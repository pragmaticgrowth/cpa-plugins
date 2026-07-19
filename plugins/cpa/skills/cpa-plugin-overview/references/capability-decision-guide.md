# Capability Decision Guide

All capabilities below are fields on `pluginapi.Capabilities` (`sdk/pluginapi/types.go`), declared
as `true`/non-nil in the JSON `capabilities` object returned from `plugin.register` /
`plugin.reconfigure`. The host's general rule: **native logic runs first, plugins fill the gaps.**
When multiple plugins can handle the same stage, higher `priority` plugins run first.

```go
// sdk/pluginapi/types.go — Capabilities struct (field names as declared)
type Capabilities struct {
    ModelRegistrar                 ModelRegistrar
    ModelProvider                  ModelProvider
    AuthProvider                   AuthProvider
    FrontendAuthProvider           FrontendAuthProvider
    FrontendAuthProviderExclusive  bool
    Scheduler                      Scheduler
    ModelRouter                    ModelRouter
    Executor                       ProviderExecutor
    ExecutorModelScope             ExecutorModelScope
    ExecutorInputFormats           []string
    ExecutorOutputFormats          []string
    RequestTranslator              RequestTranslator
    RequestNormalizer              RequestNormalizer
    ResponseTranslator             ResponseTranslator
    ResponseBeforeTranslator       ResponseNormalizer
    ResponseAfterTranslator        ResponseNormalizer
    RequestInterceptor             RequestInterceptor
    ResponseInterceptor            ResponseInterceptor
    StreamChunkInterceptor         StreamChunkInterceptor
    ThinkingApplier                ThinkingApplier
    UsagePlugin                    UsagePlugin
    CommandLinePlugin              CommandLinePlugin
    ManagementAPI                  ManagementAPI
}
```

## Startup / model-registry capabilities

### `model_registrar` — fixed, static model catalogue
**Intent:** "I have a small, hardcoded, version-pinned model list that never changes per
user/credential." **Why this one:** `model.register` returns the plugin's **complete** static
model set each call (not a delta). The host skips invalid entries (empty `Provider` or `ID`). If
these models should be served *only* by this plugin's own executor, also declare `Executor` and
set an appropriate `ExecutorModelScope`. Prefer `ModelProvider` instead if the list can differ by
account or needs a live upstream call.

### `model_provider` — static + dynamic (per-credential) model discovery
**Intent:** "My model list depends on which OAuth account/credential is active, or must be
fetched live from an upstream API." **Why this one:** `model.static` returns account-independent
models; `model.for_auth` returns models tied to one credential record and may return an
`AuthUpdate` (e.g. account info, project ID, next-refresh timestamp) discovered in the same call.
Use `host.http.*` from inside this hook so proxy settings/transport policy/request logging stay
host-managed. If paired with `Executor`, `ExecutorModelScope` controls which registration path
(`"both"`/`"static"`/`"oauth"`) is active.

## Auth capabilities (backend vs frontend — do not conflate)

### `auth_provider` — upstream credential lifecycle
**Intent:** "I'm adding a brand-new upstream that needs OAuth, device-code, API-key-file, or a
custom JSON credential format, and I want CLIProxyAPI to manage storage/refresh natively." **Why
this one:** `ParseAuth`, `StartLogin`, `PollLogin`, `RefreshAuth` hook into the host's credential
file lifecycle instead of the plugin holding secrets itself. This is a **backend/upstream**
concern.

### `frontend_auth_provider` (+ `frontend_auth_provider_exclusive`)
**Intent:** "I want to control who is allowed to call CLIProxyAPI itself" — e.g. a custom header
scheme, mTLS-derived identity, or an external auth service. **Why this one:** `Authenticate(ctx,
FrontendAuthRequest)` runs **before** proxy routing — the inverse concern from `auth_provider`.
Add the `frontend_auth_provider_exclusive` flag only when you want to fully **replace**
CLIProxyAPI's built-in API-key auth and every other frontend-auth plugin (e.g. an org SSO/mTLS
gateway) rather than layer on top of it.

## Routing & scheduling capabilities

### `model_router` — pre-provider/pre-auth routing decisions
**Intent:** "A request needs content-based redirection (specific tool-call shape, model-name
pattern, etc.) *before* the host resolves model→provider and *before* auth/credential selection
runs." **Why this one:** it is the **earliest** interception point among all capabilities — ahead
of normal model→provider lookup and ahead of auth selection. `ModelRouteResponse.TargetKind`
picks one of `ModelRouteTargetSelf` (this plugin's own `Executor`), `ModelRouteTargetExecutor`
(another named plugin's executor), or `ModelRouteTargetProvider` (a built-in provider, optionally
rewriting the model via `TargetModel`). An unready/invalid target is treated as unhandled, so
routing falls through to the next-priority router instead of committing to something that would
500 later.

### `scheduler` — auth/credential candidate selection
**Intent:** "I want to choose which credential (`AuthID`) services a request myself, instead of
(or before) the built-in round-robin/fill-first scheduler." **Why this one:** `Pick(ctx,
SchedulerPickRequest)` runs ahead of the built-in scheduler; return a specific `AuthID`, or return
`DelegateBuiltin` (`pluginapi.SchedulerBuiltinRoundRobin` / `SchedulerBuiltinFillFirst`) to hand
the decision back. This is a pre-routing hook over **auth selection**, distinct from
`model_router`'s pre-routing hook over **provider/target selection**.

## Executor capability

### `executor` — upstream/backend request execution
**Intent:** "I'm adding a genuinely new upstream/backend the host doesn't already know how to
talk to, and I need to make the actual HTTP call myself." **Why this one:** described upstream as
"the capability closest to an upstream adapter" — `Execute`, `ExecuteStream`, `CountTokens`,
`HttpRequest`. Also used as the `Self` target of a `model_router` plugin, or bound to models
registered via `model_registrar`/`model_provider`. Must declare at least one
`ExecutorInputFormats` and one `ExecutorOutputFormats` entry — the host uses these to decide
whether the executor can directly satisfy a request without going through the shared translator
registry (`executorAdapter.selectExecutorInputFormat`/`selectExecutorOutputFormat`).

## Request/response transform capabilities

The crisp distinction (the single most important thing to get right — these three families look
superficially similar but have very different host-side selection semantics):

| | **Translator** | **Normalizer** | **Interceptor** |
|---|---|---|---|
| Job | Convert between two *different* wire protocols. Format-changing. | Rewrite a payload that stays in the *same* protocol shape — fill defaults, fix quirks. Format-preserving. | Rewrite headers and/or body around the edges of execution. Not a translation concern; can touch `http.Header`. |
| Selection (multiple plugins) | **First match wins** — first non-empty `Body` short-circuits the loop. | **All run, chained** — each plugin's output feeds the next as input. | **All run, chained** — headers merged (`mergeHeaders`; `ClearHeaders` deletes), body replaced whenever non-empty. |
| Silent failure | Yes — error/empty `Body` means the host tries the next candidate, or falls back to the original body untranslated. | Yes — skips that stage, previous value passes through unchanged. | Yes — keeps `current` as it stood, moves to the next plugin. |
| Touches HTTP headers? | No — `Body` only. | No — `Body` only. | **Yes** — the only family with `Headers`/`ClearHeaders`. |

In one sentence each:
- **Translator** = "I speak protocol A, the other side speaks protocol B, let me convert."
- **Normalizer** = "The payload is already in the right protocol shape, but it's slightly
  broken/incomplete — let me patch it."
- **Interceptor** = "I want to observe/mutate the request or response envelope (headers + body) at
  a specific point in the execution lifecycle," including points that have nothing to do with
  cross-protocol translation.

### `request_translator`
**Intent:** "I need to convert a canonical (host-normalized) request body into my upstream
provider's wire payload." **Why this one:** runs in the protocol-translation stage right before
request execution; `TranslateRequest(ctx, RequestTransformRequest{FromFormat, ToFormat, Model,
Stream, Body}) (PayloadResponse, error)`. **Fallback only** — invoked when no built-in `from→to`
transform function is already registered for that pair.

### `response_translator`
**Intent:** "I need to convert a canonical/upstream response back into the client-requested
protocol." **Why this one:** the symmetric counterpart of `request_translator`, running after the
upstream response returns and before it's sent to the client; also **fallback-only**, first-match
wins. `TranslateResponse` receives both `OriginalRequest` (raw client body) and
`TranslatedRequest` (what was actually sent upstream) for cross-referencing. Streaming support is
host/format-dependent — test it explicitly.

### `request_normalizer`
**Intent:** "The request is already in the right protocol shape but needs defaults filled or a
provider quirk patched, and this should apply to *every* translation, always." **Why this one:**
`NormalizeRequest` is chained (not first-match) and **always-on** — every declared normalizer
runs, feeding its output to the next.

### `response_before_translator`
**Intent:** "The raw upstream provider-native response has missing/non-standard fields I need to
fix *before* any response translation (host-native or plugin) runs." **Why this one:**
`NormalizeResponse` (via the shared `ResponseNormalizer` interface) fires pre-translation, chained.
Don't output client-protocol format here unless the current stage's `ToFormat` already *is* the
client format — that's the translator's job.

### `response_after_translator`
**Intent:** "The response has already been translated into the client's protocol, and I need one
final compatibility pass — filling a required-but-missing field, a strict-client shim." **Why this
one:** `NormalizeResponse` fires post-translation, chained, always-on. Do not call upstream again
here and do not change billing semantics. To change HTTP response **headers** (not body), use
`response_interceptor` instead — normalizers never see `http.Header`. A single plugin can
implement both `response_before_translator` and `response_after_translator` (they share the same
`ResponseNormalizer` Go interface; the host distinguishes by which struct field/RPC method it
calls).

### `request_interceptor`
**Intent:** "I need to inject/rewrite headers or body around execution — auth header injection,
feature-flag headers, request logging tied to credential context — something that isn't strictly
a protocol-format concern." **Why this one:** the only request-side family that operates on the
full envelope (`http.Header` + `Body`), with **two distinct call sites**:
`InterceptRequestBeforeAuth` (pre credential-selection, `ToFormat` may still be empty) and
`InterceptRequestAfterAuth` (post credential-selection, `Model`/`ToFormat` now concrete).

### `response_interceptor`
**Intent:** "I need to rewrite headers/body of a completed, successful, **non-streaming**
response right before delivery to the client." **Why this one:** `InterceptResponse` is chained
(headers merged via `mergeHeaders`/`ClearHeaders`; body replaced when non-empty). **Non-streaming
only** — never invoked on a streaming response; use `response_stream_interceptor` for that.

### `response_stream_interceptor`
**Intent:** "I need to rewrite or drop individual SSE/streaming chunks, or adjust stream response
headers at initialization." **Why this one:** `InterceptStreamChunk` sees `ChunkIndex == -1`
(`StreamChunkHeaderInitIndex`) as a **header-only init call** (your one chance to adjust headers
before any chunk streams) and `ChunkIndex >= 0` as real payload chunks. `HistoryChunks` gives a
**bounded** rolling window (up to 64 chunks / 1 MiB total) — not a full replay buffer. Setting
`DropChunk: true` suppresses that chunk (header changes still apply) and skips remaining
interceptors in that chunk's chain. Do not make high-latency external calls per chunk (hot
streaming path); preserve SSE frame boundaries (`data:` lines, blank-line separators, `[DONE]`
markers) when rewriting `Body`.

## Extension capabilities

### `thinking_applier`
**Intent:** "I own a plugin-defined provider and need to write the host's already-parsed,
already-validated canonical thinking/reasoning config into my provider's own payload field
shape." **Why this one:** it is explicitly the **last mile only** — the host
(`internal/thinking/`) parses suffixes (`-thinking-high`, budget numbers), normalizes, and
validates into a canonical `ThinkingConfig` *before ever calling the plugin*; plugins never
reimplement that parsing/validation. A plugin can own thinking-application for its own provider
key (registered via `ThinkingApplier.Identifier()`), but cannot override a **native** provider's
thinking behavior — native keys win when both exist.

### `usage_plugin`
**Intent:** "I want to observe token counts, latency, TTFT, and billing metadata for external
stats/audit/billing systems, without altering requests or responses." **Why this one:**
`HandleUsage(ctx, UsageRecord)` is a fire-and-forget side channel, called once per completed
request (success or failure). Not for mutating anything.

### `command_line_plugin`
**Intent:** "I want to add my own CLI flags to the `cli-proxy-api` binary — a login flow,
credential import/export, a diagnostic — that runs once and exits." **Why this one:**
`RegisterCommandLine`/`ExecuteCommandLine` hook into the process's flag parsing/startup path. Not
for long-running server tasks.

### `management_api`
**Intent:** "I want an admin UI panel or a programmatic control surface for my plugin — status,
diagnostics, config." **Why this one:** `RegisterManagement` lets a plugin register (a)
authenticated JSON API routes under the host's Management API, and (b) unauthenticated
browser-navigable "resource" pages under a plugin-namespaced path
(`/v0/management/*`, `/v0/resource/plugins/<id>/*`).

## Quick lookup by RPC method constant (`sdk/pluginabi/types.go`)

| Method constant | Capability |
|---|---|
| `model.register` | `ModelRegistrar` |
| `model.static`, `model.for_auth` | `ModelProvider` |
| `auth.identifier`, `auth.parse`, `auth.login.start`, `auth.login.poll`, `auth.refresh` | `AuthProvider` |
| `frontend_auth.identifier`, `frontend_auth.authenticate` | `FrontendAuthProvider` |
| `scheduler.pick` | `Scheduler` |
| `model.route` | `ModelRouter` |
| `executor.identifier`, `executor.execute`, `executor.execute_stream`, `executor.count_tokens`, `executor.http_request` | `Executor` (`ProviderExecutor`) |
| `request.translate` | `RequestTranslator` |
| `request.normalize` | `RequestNormalizer` |
| `request.intercept_before`, `request.intercept_after` | `RequestInterceptor` |
| `response.translate` | `ResponseTranslator` |
| `response.normalize_before` | `ResponseBeforeTranslator` |
| `response.normalize_after` | `ResponseAfterTranslator` |
| `response.intercept_after` | `ResponseInterceptor` |
| `response.intercept_stream_chunk` | `StreamChunkInterceptor` |
| `thinking.identifier`, `thinking.apply` | `ThinkingApplier` |
| `usage.handle` | `UsagePlugin` |
| `command_line.register`, `command_line.execute` | `CommandLinePlugin` |
| `management.register`, `management.handle` | `ManagementAPI` |

Full per-capability request/response type definitions:
`${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginapi-types.go`. Prose spec per capability:
`${CLAUDE_PLUGIN_ROOT}/references/upstream/docs-plugin/<capability-name>.md`.
