---
name: cpa-capabilities
description: Use when building or reviewing a CLIProxyAPI native plugin's capability declarations — choosing which of the ~15 CLIProxyAPI plugin capabilities (model_registrar, model_provider, model_router, auth_provider, frontend_auth_provider[_exclusive], executor, request/response translators, normalizers, interceptors, thinking_applier, scheduler, usage_plugin, command_line_plugin, management_api, host callbacks) to implement, distinguishing translator vs normalizer vs interceptor, or wiring capability flags/methods/config for a CLIProxyAPI plugin.
---

# CPA Capabilities

CLIProxyAPI plugins declare capabilities in the `capabilities` map returned from `plugin.register`/
`plugin.reconfigure`. Each capability is one or more Go interfaces on `pluginapi.Capabilities` plus
one or more RPC methods named as constants in `pluginabi`. **Declare only what you implement** — an
undeclared capability is simply never called by the host.

Host rule everywhere: **native logic runs first, plugins fill gaps**; among competing plugins for
the same stage, higher `plugins.configs.<id>.priority` wins (descending; ties → ascending plugin
ID string, `sortRecords` in `internal/pluginhost/snapshot.go`).

Ground truth: `${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginapi-types.go` (interfaces/structs,
the `Capabilities` struct), `${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginabi-types.go` (RPC
method constants + `{ok,result,error}` envelope). Each capability also has an authoritative
upstream doc at `${CLAUDE_PLUGIN_ROOT}/references/upstream/docs-plugin/<name>.md`. Pinned to
upstream **v7.2.88**. Every capability below has its own single-purpose vendored Go example under
`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/<dir>/go/main.go` — start there, not from
`examples/simple`, which is a schema/method-name showcase with trivial stub handlers (and does
**not** implement `model_router`, `scheduler`, or any of the three interceptors).

## Translator vs Normalizer vs Interceptor — the crisp distinction

- **Translator** (`request_translator`, `response_translator`) — converts between two *different*
  wire protocols (format-changing, e.g. `chat-completions` → `anthropic`). Multiple declared →
  **first plugin returning a non-empty `Body` wins**; the rest are never called for that request.
- **Normalizer** (`request_normalizer`, `response_before_translator`, `response_after_translator`)
  — rewrites a payload that stays in the *same* protocol shape (format-preserving: fill defaults,
  patch quirks). Multiple declared → **all run, chained**, each seeing the prior one's output.
- **Interceptor** (`request_interceptor`, `response_interceptor`, `response_stream_interceptor`) —
  rewrites **headers and/or body** at a specific lifecycle point (before/after credential
  selection; final non-streaming response; per streaming chunk). The *only* family touching
  `http.Header`. Multiple declared → **all run, chained**, headers merged + body replaced when
  non-empty.

In short: translator = "I convert protocol A to B." Normalizer = "same protocol, let me patch it."
Interceptor = "let me touch headers+body at this point in the lifecycle, translation-agnostic."

Full depth (host dispatch code, every wire example, all request/response types, thinking applier
provider-registry semantics): `${CLAUDE_PLUGIN_ROOT}/skills/cpa-capabilities/references/translate-normalize-intercept.md`.

## Family 1 — Providers, Auth, Router, Executor

| Capability | Purpose | Key method(s) | When to use | Vendored example |
|---|---|---|---|---|
| `model_registrar` | Register a fixed, static model catalogue. | `model.register` | Model list never depends on which credential/account is active. | `examples/simple/go` |
| `model_provider` | Static **and** per-credential (OAuth/file) model discovery. | `model.static`, `model.for_auth` | Model set varies by logged-in account, or must be fetched live from upstream. | `examples/model/go` |
| `model_router` | Pre-provider/pre-auth routing decision — earliest interception point in the request pipeline. | `model.route` | Content/header-based routing to own executor, another plugin's executor, or a built-in provider, before credential selection. | `examples/claude-web-search-router/go` |
| `auth_provider` | Upstream credential parse/login/poll/refresh. | `auth.identifier`, `auth.parse`, `auth.login.start`, `auth.login.poll`, `auth.refresh` | New upstream needing OAuth/device-code/API-key-file credential lifecycle management. | `examples/auth/go` |
| `frontend_auth_provider` | Authenticate inbound *client* requests before proxy flow. | `frontend_auth.identifier`, `frontend_auth.authenticate` | Custom client-facing auth scheme layered alongside built-in API-key auth. | `examples/frontend-auth/go` |
| `frontend_auth_provider_exclusive` | Add-on flag on `frontend_auth_provider`; makes this plugin the *sole* frontend auth source when selected. | (same methods as above) | Fully replace CLIProxyAPI's frontend auth (SSO/mTLS gateway). Never declare alone — meaningless without the base flag. | `examples/frontend-auth-exclusive/go` |
| `executor` | Executes the actual upstream/backend HTTP call. | `executor.identifier`, `executor.execute`, `executor.execute_stream`, `executor.count_tokens`, `executor.http_request` | Genuinely new upstream backend the host can't already talk to. Must declare ≥1 input **and** ≥1 output format. | `examples/executor/go` (also `examples/protocol-format/go` for a format-translating executor: `chat-completions` in → `responses` out) |

Depth (config keys, `ExecutorModelScope`, `ModelRouteTargetKind`, composition rules across these
capabilities): `${CLAUDE_PLUGIN_ROOT}/skills/cpa-capabilities/references/providers-auth-exec.md`.

## Family 2 — Translate / Normalize / Intercept (+ Thinking Applier)

| Capability | Purpose | Key method(s) | When to use | Vendored example |
|---|---|---|---|---|
| `request_translator` | Canonical request → upstream protocol payload. | `request.translate` | Format conversion right before execution. | `examples/request-translator/go` (trivial echo — shows shape, not logic) |
| `request_normalizer` | Clean/default a same-format request. | `request.normalize` | Lightweight rewrite before execution — e.g. force a service tier field. | `examples/codex-service-tier/go` (real, worked example); `examples/request-normalizer/go` (trivial echo) |
| `response_translator` | Upstream provider payload → client/canonical protocol. | `response.translate` | Symmetric response-side format conversion, post-upstream-call. | `examples/response-translator/go` (trivial echo) |
| `response_before_translator` | Patch a raw provider-native response before translation. | `response.normalize_before` | Fix missing/non-standard upstream fields pre-translation. | `examples/response-normalizer/go` (implements both before+after in one plugin) |
| `response_after_translator` | Final polish after response translation. | `response.normalize_after` | Client-protocol compatibility shims, required-but-missing fields. Header changes go through an interceptor instead. | `examples/response-normalizer/go` |
| `request_interceptor` | Rewrite headers/body before/after credential selection. | `request.intercept_before`, `request.intercept_after` | Auth-header injection, feature-flag headers, credential-context-dependent rewrites. | no dedicated example — implement from the interface directly (see `pluginapi-types.go`: `RequestInterceptor`) |
| `response_interceptor` | Rewrite headers/body of a successful **non-streaming** response. | `response.intercept_after` | Last-chance header/body rewrite pre-client, non-streaming only. | no dedicated example — implement from the interface directly (see `pluginapi-types.go`: `ResponseInterceptor`) |
| `response_stream_interceptor` | Rewrite/drop individual SSE chunks; adjust stream headers at init. | `response.intercept_stream_chunk` | Per-chunk streaming rewrite; `ChunkIndex == -1` is the header-init call. | no dedicated example — implement from the interface directly (see `pluginapi-types.go`: `StreamChunkInterceptor`) |
| `thinking_applier` | Write canonical thinking config into a provider payload (last mile only). | `thinking.identifier`, `thinking.apply` | Plugin-defined provider needs `-thinking-*` config translated into its own JSON fields. Registry is provider-keyed; native providers always win over plugins. | `examples/thinking/go` |

Depth (host dispatch code per capability, thinking-applier provider-registry tie-breaking, 10
cross-cutting gotchas): `${CLAUDE_PLUGIN_ROOT}/skills/cpa-capabilities/references/translate-normalize-intercept.md`.

## Family 3 — Ops: Scheduler, Usage, CLI, Management API, Host Callbacks

| Capability | Purpose | Key method(s) | When to use | Vendored example |
|---|---|---|---|---|
| `scheduler` | Pick a credential (`AuthID`) before the built-in scheduler runs, or delegate to it. | `scheduler.pick` | Custom auth-selection logic (e.g. pin to one account, deny a request). Only one scheduler plugin runs per request (first by priority order). | `examples/scheduler/go` |
| `usage_plugin` | Fire-and-forget observer of one `UsageRecord` per completed request. | `usage.handle` | External stats/billing/audit systems. Never alters requests/responses; every declaring plugin gets every record (no single-winner). | `examples/usage/go` |
| `command_line_plugin` | Register plugin-owned CLI flags on the `cli-proxy-api` binary. | `command_line.register`, `command_line.execute` | One-shot CLI actions: login flows, credential import/export, diagnostics. Not for long-running tasks. | `examples/cli/go` |
| `management_api` | Authenticated Management API routes + unauthenticated browser resource pages. | `management.register`, `management.handle` | Plugin admin/status UI or programmatic control surface. Resource pages get no HTML sanitization — you own XSS safety. | `examples/management-api/go` (also `examples/host-callback-auth-files/go`, `examples/host-model-callback/go`) |
| host callbacks (no capability flag — always available) | Plugin → host RPC: HTTP, model execution, credential files, streaming, logging. | `host.http.*`, `host.model.*`, `host.auth.*`, `host.stream.*`, `host.log` | Reuse host-managed proxy/logging/usage plumbing instead of reimplementing it. Forward `host_callback_id` on nested `host.model.*` calls to avoid self-recursion. | `examples/host-callback/go` (HTTP + log), `examples/host-callback-auth-files/go` (credential file callbacks), `examples/host-model-callback/go` (model execution + recursion guard) |

Depth (host dispatch internals, `host_callback_id` recursion guard, config examples, the
`host-callback-auth-files` unauthenticated-write anti-pattern to avoid):
`${CLAUDE_PLUGIN_ROOT}/skills/cpa-capabilities/references/ops-scheduler-usage-cli-mgmt.md`.

## Cross-cutting rules for every capability

- **Panics permanently "fuse" a plugin.** Every dispatch path wraps the plugin call in
  `recover()`; on panic, `h.fusePlugin(id, method, recovered)` disables that plugin instance for
  every future capability call, for the rest of the process's life. Never let a panic escape;
  return an error instead.
- **Empty `Body`/error is the load-bearing "not handled" signal** across translators, normalizers,
  and interceptors — it means "pass through unchanged," not "clear the content."
- Plugin-specific config arrives as raw `config_yaml` bytes inside **both** `plugin.register` and
  `plugin.reconfigure` — parse/cache it in both. Only `enabled`/`priority` are host-reserved keys
  under `plugins.configs.<id>`.
- `examples/simple/go/main.go` (vendored at
  `${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/simple/go/main.go`) is a single plugin that
  turns on most capability flags at once and shows most request/response types in one `switch` —
  but it does **not** implement `model_router`, `scheduler`, `frontend_auth_provider_exclusive`,
  or any of the three interceptors, and every handler is an intentionally trivial stub, not real
  logic. Use it to see the RPC shapes, then move to a capability's own dedicated example (table
  above) for a realistic single-purpose plugin, or `examples/codex-service-tier/go` /
  `examples/claude-web-search-router/go` for genuinely worked real-world logic.
