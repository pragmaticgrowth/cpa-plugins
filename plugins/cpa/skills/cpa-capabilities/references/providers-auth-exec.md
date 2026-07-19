# Providers, Auth, Router, Executor — depth reference

Covers: `model_registrar`, `model_provider`, `model_router`, `auth_provider`,
`frontend_auth_provider` / `frontend_auth_provider_exclusive`, `executor`.

Ground truth: `${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginapi-types.go`,
`${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginabi-types.go`, and per-capability docs under
`${CLAUDE_PLUGIN_ROOT}/references/upstream/docs-plugin/`. Pinned to upstream **v7.2.88**.

Host rule everywhere in this family: **native logic runs first, plugins fill gaps**; among
competing plugins for the same stage, higher `priority` (`plugins.configs.<id>.priority`) wins.

---

## 1. `model_registrar` — static model metadata registration

**Purpose:** Registers a fixed, static set of model metadata into the CLIProxyAPI model registry.
For plugins whose model list never depends on which credential/account is active.

**Capability flag:**

```json
{ "capabilities": { "model_registrar": true } }
```

**Method:**

| Method | Purpose |
|---|---|
| `model.register` | Called after the host loads/reconfigures the plugin, during model registration. Returns the plugin's static models. |

**Request** (`ModelRegistrationRequest`):

```json
{ "Plugin": { "Name": "example", "Version": "0.1.0", "Author": "router-for-me" } }
```

`Plugin` carries the plugin's own metadata so it can decide what models to return based on its own
version/configuration.

**Response** (`ModelRegistrationResponse`):

```json
{
  "Provider": "plugin-example",
  "Models": [
    {
      "ID": "plugin-example-model",
      "Object": "model",
      "OwnedBy": "plugin-example",
      "DisplayName": "Plugin Example Model",
      "SupportedGenerationMethods": ["chat"],
      "ContextLength": 8192,
      "MaxCompletionTokens": 1024,
      "UserDefined": true
    }
  ]
}
```

**Key semantics:**

- `Provider` must be a stable provider identifier.
- `Models` is the **complete** model set each call — not a delta/diff.
- `ID` is the exact model name clients will request.
- `Thinking` on a model declares the thinking range supported, used for thinking-config validation
  and later consumed by `thinking_applier`.
- The host **skips invalid models** — empty `Provider` or empty model `ID` is rejected.
- If these models should be handled *only* by this plugin's own executor, also declare `executor`
  and set an appropriate `executor_model_scope` (§7) — otherwise the host treats them as ordinary
  plugin-provided model clients without executor binding.

**Relationship to `model_provider`:** "A model registrar only handles static models. Use the model
provider capability when models must be discovered dynamically per OAuth or file credential."

**When to use:** A small, hardcoded, version-pinned model catalogue (e.g. a fixed list of models a
self-hosted backend supports) that doesn't change per user/credential.

**Vendored example:** `${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/simple/go/main.go` —
`pluginabi.MethodModelRegister` case (trivial stub; still the shape reference for this method).

**Source refs:** `sdk/pluginapi/types.go`: `ModelRegistrar`, `ModelRegistrationRequest`,
`ModelRegistrationResponse`, `ModelInfo`. `sdk/pluginabi/types.go`: `model.register`.
`internal/pluginhost/adapters.go`: `RegisterModels`, `callModelRegistrar`.

---

## 2. `model_provider` — static + dynamic (per-credential) model discovery

**Purpose:** Provides static model lists **and** dynamically discovers models tied to a specific
credential record. "It is a better fit than a model registrar for OAuth, file credentials, or
plugins that need to access upstream model lists."

**Capability flag:**

```json
{ "capabilities": { "model_provider": true } }
```

**Methods:**

| Method | Purpose |
|---|---|
| `model.static` | Returns static model lists that do not depend on a specific credential. |
| `model.for_auth` | Returns model lists for a credential record; may also return credential updates. |

**`model.static` request** (`StaticModelRequest`):

```json
{ "Plugin": {}, "Host": { "AuthDir": "~/.cli-proxy-api", "ProxyURL": "", "ForceModelPrefix": false } }
```

**`model.for_auth` request** (`AuthModelRequest`):

```json
{
  "AuthID": "auth-1",
  "AuthProvider": "plugin-example",
  "StorageJSON": "base64-json",
  "Metadata": {},
  "Attributes": {},
  "Host": {}
}
```

If the plugin must call an upstream model-listing API from here, use the `host.http.*` bridge tied
to the host HTTP client so proxy settings, transport policy, and request logging stay
host-managed.

**Response (shared by both methods)** (`ModelResponse`):

```json
{
  "Provider": "plugin-example",
  "Models": [ { "ID": "plugin-example-model", "Object": "model", "OwnedBy": "plugin-example",
    "DisplayName": "Plugin Example Model", "SupportedGenerationMethods": ["chat"],
    "ContextLength": 8192, "MaxCompletionTokens": 1024, "UserDefined": true } ],
  "AuthUpdate": {}
}
```

`AuthUpdate` lets model discovery push credential updates back (account info, project ID, or a
next-refresh timestamp returned by upstream during the same call that lists models).

**Relationship with executors:** If the plugin also declares `executor`, `executor_model_scope`
controls which registration path is active for this model provider:

| Value | Meaning |
|---|---|
| `static` | Only registers static models. |
| `oauth` | Only handles models discovered by credential. |
| `both` (or empty) | Supports both. |

**Development notes:**

- `model.for_auth` should only handle credential providers it actually recognizes.
- If the response's `Provider` is empty, the host falls back to the provider of the current
  credential.
- Returning an error from dynamic discovery makes the host treat discovery for that credential as
  "handled but failed" (not "unhandled" — so it won't fall through to another provider).

**When to use:** Any upstream where the available model list depends on the logged-in account
(OAuth scopes/tier), or where models must be fetched live from an upstream API, or where a mix of
fixed + per-account models exists.

**Vendored example:** `${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/model/go/main.go` — the
dedicated single-purpose example, capability flag `"capabilities":{"model_provider":true}`, handles
`pluginabi.MethodModelStatic` and `pluginabi.MethodModelForAuth`.

**Source refs:** `sdk/pluginapi/types.go`: `ModelProvider`, `StaticModelRequest`,
`AuthModelRequest`, `ModelResponse`. `sdk/pluginabi/types.go`: `model.static`, `model.for_auth`.
`internal/pluginhost/adapters.go`: `RegisterModels`, `ModelsForAuth`.

---

## 3. `model_router` — pre-provider/pre-auth routing decisions

**Purpose:** Lets a plugin decide **where a matching model request should execute** before the
host resolves the requested model to a provider and before auth (credential) selection runs. This
is the earliest interception point in the request-entry pipeline among the capabilities in this
file — "The host asks enabled model routers before the normal model-to-provider lookup and auth
selection."

Use it when routing depends on request content, headers, query params, or the client's
originally-requested model, and the destination could be:

- the router plugin's own executor,
- another plugin's executor,
- or a built-in provider path (`codex`, `antigravity`, `xai`, `claude`, etc.).

**Capability flag:**

```json
{ "capabilities": { "model_router": true } }
```

If the router can also route to its own executor, it must also declare `executor` alongside it:

```json
{
  "capabilities": {
    "model_router": true,
    "executor": true,
    "executor_model_scope": "static",
    "executor_input_formats": ["claude"],
    "executor_output_formats": ["claude"]
  }
}
```

**Method:**

| Method | Purpose |
|---|---|
| `model.route` | Returns a routing decision for the current client request. |

**When it runs:** Higher-`priority` router plugins run first. A router that returns
`Handled: false`, an invalid target, or an unavailable target is skipped and the host tries the
next router; if none handle it, the normal host path continues. The request still carries the
**original client protocol** — e.g. a Claude-compatible request arrives with
`SourceFormat: "claude"` and the raw Claude body in `Body`.

**Request** (`ModelRouteRequest`):

```json
{
  "Plugin": {},
  "PluginID": "claude-web-search-router",
  "SourceFormat": "claude",
  "RequestedModel": "claude-sonnet-4-6",
  "Stream": true,
  "Headers": {},
  "Query": {},
  "Body": "base64-client-body",
  "Metadata": {},
  "AvailableProviders": ["antigravity", "codex", "xai"]
}
```

| Field | Description |
|---|---|
| `PluginID` | Host-local ID of the router plugin being called. |
| `SourceFormat` | Original client protocol: `openai`, `claude`, `gemini`, etc. |
| `RequestedModel` | Client-requested model before provider/auth resolution. |
| `Stream` | Whether the client expects streaming. |
| `Headers`/`Query` | Inbound request headers/query params. |
| `Body` | Raw client body, base64 in the JSON RPC envelope. |
| `Metadata` | Best-effort request context snapshot; treat as read-only. |
| `AvailableProviders` | Built-in provider keys that currently have registered auth — must be checked before returning a `provider` target. |

**Response** (`ModelRouteResponse`), three shapes:

Not handled:

```json
{ "Handled": false }
```

Route to this plugin's own executor:

```json
{ "Handled": true, "TargetKind": "self", "Reason": "matched_web_search" }
```

Route to another plugin's executor:

```json
{ "Handled": true, "TargetKind": "executor", "Target": "search-executor", "Reason": "matched_search_executor" }
```

Route to a built-in provider:

```json
{
  "Handled": true,
  "TargetKind": "provider",
  "Target": "codex",
  "TargetModel": "gpt-5.4-mini",
  "Reason": "matched_codex_web_search"
}
```

**`TargetKind` table:**

| TargetKind | Target | TargetModel | Behavior |
|---|---|---|---|
| `self` | ignored — host uses current router plugin ID | ignored | Executes the router plugin's own executor. |
| `executor` | target plugin ID | ignored | Executes another plugin's executor directly. |
| `provider` | built-in provider key | optional model override | Continues through the built-in AuthManager + provider executor path. |

Constraints: direct plugin-executor routes (`self`/`executor`) skip auth-record selection
entirely — the target executor must declare `executor` with `executor_model_scope: "static"` or
`"both"`, and must support the request/response protocol formats of the current request. Provider
routes must target a provider present in `AvailableProviders`; if `TargetModel` is empty the host
keeps the client's original requested model, but if the provider needs a provider-native model
name it must be set explicitly.

**Config example** (the `claude-web-search-router` example):

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    claude-web-search-router:
      enabled: true
      priority: 20
      route: fallback
      antigravity_model: "gemini-3.1-flash-lite"
      codex_model: "gpt-5.4-mini"
      xai_model: "grok-4.3"
      tavily_api_keys:
        - "tvly-xxxxxxxx"
      require_web_search_only: true
```

Route behavior table from the doc:

| Route | Target |
|---|---|
| `antigravity_google` | `TargetKind: "provider"`, `Target: "antigravity"`, `TargetModel: antigravity_model` |
| `codex_web_search` | `TargetKind: "provider"`, `Target: "codex"`, `TargetModel: codex_model` |
| `xai_web_search` | `TargetKind: "provider"`, `Target: "xai"`, `TargetModel: xai_model` |
| `tavily` | `TargetKind: "self"` — plugin executor handles Tavily itself. |
| `fallback` | `TargetKind: "self"` — plugin executor orchestrates fallback across configured backends. |

**Development notes:**

- Return `Handled: false` for anything the plugin doesn't recognize so lower-priority routers / the
  normal host path can still run.
- Keep `model.route` **fast** — it classifies and picks a target, it does not perform the upstream
  request itself.
- Check `AvailableProviders` before returning a `provider` target.
- Use `self` when the executor needs to orchestrate fallback, call `host.model.*`, or hit a
  plugin-owned external service (e.g. Tavily).
- Use `provider` when the request should go through host-managed auth selection, logging, usage
  accounting, and the built-in executor.
- `model_router` is gated purely by the capability flag + `model.route` method — no plugin schema
  version bump required.

**When to use:** Content-based routing decisions that must happen *before* credential/provider
resolution — e.g. detecting a specific tool-call shape (Claude Code's `web_search`) and redirecting
to a different backend or a plugin-owned service, with fallback logic across multiple built-in
providers.

**Vendored example:**
`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/claude-web-search-router/go/` — the only
non-trivial, real-world multi-file example in the whole set (`main.go`, `fallback.go`,
`detect.go`, `penalty.go`, `tavily.go`, `stream_forward.go`, `model_resolve.go`,
`execute_stream.go`, `execution_fallback.go`, `claude_response.go`, plus tests). Read this end to
end for a genuine `model_router` + self-owned `executor` + fallback-orchestration pattern.

**Source refs:** `sdk/pluginapi/types.go`: `ModelRouter`, `ModelRouteRequest`,
`ModelRouteResponse`, `ModelRouteTargetKind`. `sdk/pluginabi/types.go`: `model.route`.
`internal/pluginhost/model_router.go`: router priority, target validation, built-in provider
availability checks. `sdk/api/handlers/handlers.go`: request entry point before normal
provider/auth resolution.

---

## 4. `auth_provider` — credential parsing, login, refresh

**Purpose:** Lets a plugin participate in **credential file parsing, login, polling, and refresh**
— i.e. add a new upstream provider that needs OAuth, device codes, API key files, or custom JSON
credential formats. This is a backend/upstream credential concern, distinct from
`frontend_auth_provider` (which authenticates *inbound client* requests).

**Capability flag:**

```json
{ "capabilities": { "auth_provider": true } }
```

**Methods:**

| Method | Purpose |
|---|---|
| `auth.identifier` | Returns the provider identifier this plugin handles. |
| `auth.parse` | Tries to parse a credential JSON file discovered by the host. |
| `auth.login.start` | Starts a login flow; returns the URL and polling state for the user. |
| `auth.login.poll` | Polls the login flow; returns `AuthData` on success. |
| `auth.refresh` | Refreshes an existing credential; returns updated credential data plus the next refresh time. |

**`AuthData`** — the core structure exchanged between plugin and host for credential data:

```json
{
  "Provider": "plugin-example",
  "ID": "plugin-example-auth",
  "FileName": "plugin-example.json",
  "Label": "Plugin Example",
  "Prefix": "",
  "ProxyURL": "",
  "Disabled": false,
  "StorageJSON": "base64-json",
  "Metadata": {},
  "Attributes": {},
  "NextRefreshAfter": "2026-06-15T12:00:00Z"
}
```

Field responsibilities:

- `StorageJSON` — the persistent credential content, **owned by the plugin**.
- `Metadata` — host-managed but mutable metadata.
- `Attributes` — **immutable** routing/provider-related attributes.
- `NextRefreshAfter` — controls the next active refresh time scheduled by the host.

**Login flow shapes:**

`auth.login.start` response:

```json
{ "Provider": "plugin-example", "URL": "https://example.com/login", "State": "opaque-state",
  "ExpiresAt": "2026-06-15T12:05:00Z", "Metadata": {} }
```

`auth.login.poll` response (pending):

```json
{ "Status": "pending", "Message": "waiting for user confirmation" }
```

On success, `Status` is `"success"` and the response's `Auth` field is populated with `AuthData`.

**Development notes:**

- `auth.parse` **must** use `Handled` to explicitly say whether it recognizes the credential file
  (don't silently no-op).
- Use the host HTTP bridge for any upstream login/refresh calls, to avoid bypassing proxy/logging
  policy.
- **Never log** `StorageJSON`, access tokens, refresh tokens, or raw user credentials.
- If the plugin also does model discovery, it typically pairs with `model_provider` (using
  `AuthID`/`StorageJSON` from the credential to call `model.for_auth`).

**When to use:** Adding an entirely new upstream that authenticates via OAuth/device-code/API-key
-file, where CLIProxyAPI needs to manage the credential lifecycle (store, refresh, poll) natively
rather than the plugin holding secrets itself.

**Vendored example:** `${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/auth/go/main.go` — the
dedicated single-purpose example, `"capabilities":{"auth_provider":true}`.

**Source refs:** `sdk/pluginapi/types.go`: `AuthProvider`, `AuthData`, `AuthParseRequest`,
`AuthLoginStartRequest`, `AuthLoginPollRequest`, `AuthRefreshRequest`. `sdk/pluginabi/types.go`:
`auth.identifier`, `auth.parse`, `auth.login.start`, `auth.login.poll`, `auth.refresh`.
`internal/pluginhost/adapters.go`: credential parsing, refresh, host HTTP client bridging.

---

## 5. `frontend_auth_provider` — inbound client request authentication

**Purpose:** Authenticates **client requests before they enter the proxy flow**. Answers "who may
call CLIProxyAPI" — the inverse concern from `auth_provider`, which manages *upstream* credentials.
Does **not** handle upstream credential selection.

**Capability flag:**

```json
{ "capabilities": { "frontend_auth_provider": true } }
```

**Methods:**

| Method | Purpose |
|---|---|
| `frontend_auth.identifier` | Returns the stable identifier of this frontend auth provider. |
| `frontend_auth.authenticate` | Decides whether authentication succeeds, from the HTTP request content. |

**Request:**

```json
{ "Method": "POST", "Path": "/v1/chat/completions", "Headers": { "Authorization": ["Bearer ..."] },
  "Query": {}, "Body": "base64-body" }
```

**Response:**

```json
{ "Authenticated": true, "Principal": "user-or-client-id",
  "Metadata": { "provider": "example-frontend-auth-go" } }
```

`Principal` is the authenticated subject; `Metadata` can carry identity attributes for downstream
consumption.

**Relationship with built-in API keys:** A normal (non-exclusive) frontend auth provider **works
alongside** existing host auth methods (e.g. built-in API keys). Only when
`frontend_auth_provider_exclusive` is also declared does the plugin become the *sole* frontend auth
source once selected (§6).

**Development notes:**

- Only authenticate client requests — don't read or return upstream credentials here.
- Be careful with body size / sensitive info when authenticating off the body; **never log the raw
  body**.
- On `Authenticated: false`, the host continues through the rest of the auth chain (non-exclusive)
  or rejects the request (exclusive mode), depending on configuration.

**When to use:** Custom client-facing auth schemes — e.g. a custom header scheme, mTLS-derived
identity, or an external auth service — layered on top of or instead of CLIProxyAPI's built-in API
key auth.

**Vendored example:**
`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/frontend-auth/go/main.go` — the dedicated
single-purpose example, `"capabilities":{"frontend_auth_provider":true}`.

**Source refs:** `sdk/pluginapi/types.go`: `FrontendAuthProvider`, `FrontendAuthRequest`,
`FrontendAuthResponse`. `sdk/pluginabi/types.go`: `frontend_auth.identifier`,
`frontend_auth.authenticate`. `internal/pluginhost/adapters.go`: `RegisterFrontendAuthProviders`.

---

## 6. `frontend_auth_provider_exclusive` — exclusive frontend auth mode

**Purpose:** Not a standalone interface — an **additional flag** on a `frontend_auth_provider`
plugin. When this plugin is selected as exclusive, the host uses **only** this plugin as the
frontend request authentication source (built-in API keys and other frontend-auth plugins are
bypassed).

**Capability flag** (must be declared together with `frontend_auth_provider`):

```json
{ "capabilities": { "frontend_auth_provider": true, "frontend_auth_provider_exclusive": true } }
```

The example plugin's Go struct wires it plainly:

```go
type capabilities struct {
    FrontendAuthProvider          bool `json:"frontend_auth_provider"`
    FrontendAuthProviderExclusive bool `json:"frontend_auth_provider_exclusive"`
}
// ...
Capabilities: capabilities{
    FrontendAuthProvider:          true,
    FrontendAuthProviderExclusive: true,
},
```

**Selection rules:**

- Only effective for plugins that **also** declare `frontend_auth_provider` — declaring only the
  exclusive flag does nothing ("Do not declare only `frontend_auth_provider_exclusive`. Without
  `frontend_auth_provider`, the field does not create a valid frontend authentication provider.").
- When multiple exclusive plugins exist, the higher-`priority` one wins.
- Equal priority → host uses a stable (deterministic) selection rule.
- When the winning exclusive plugin is removed/disabled, the host clears exclusive state (falls
  back to normal chain).

**Request/response:** Still uses `frontend_auth.authenticate` (same shape as §5). Example response:

```json
{ "Authenticated": true, "Principal": "example-frontend-auth-exclusive-go",
  "Metadata": { "mode": "exclusive", "provider": "example-frontend-auth-exclusive-go" } }
```

The example plugin's discriminating logic is driven by a custom header:
`X-Example-Frontend-Auth: exclusive`.

**Development notes:**

- Exclusive mode changes the **overall** frontend auth boundary for the whole host — enable
  carefully (it can lock out built-in API key auth entirely).
- On failure, return `Authenticated: false` — never panic or exit the process.

**When to use:** Replacing CLIProxyAPI's entire frontend authentication mechanism with a fully
custom scheme (e.g. an organization's SSO/mTLS gateway) rather than layering on top of it.

**Vendored example:**
`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/frontend-auth-exclusive/go/main.go` — the
dedicated single-purpose example.

**Source refs:** `sdk/pluginapi/types.go`: `FrontendAuthProviderExclusive`.
`internal/pluginhost/rpc_schema.go`: `frontend_auth_provider_exclusive`.
`internal/pluginhost/adapters.go`: exclusive frontend authentication provider selection logic.

---

## 7. `executor` — upstream/backend request execution

**Purpose:** Sends model requests to an upstream provider or local backend. "The capability closest
to an upstream adapter" — this is where the actual HTTP call to the model backend happens.

**Capability flag:**

```json
{
  "capabilities": {
    "executor": true,
    "executor_model_scope": "both",
    "executor_input_formats": ["chat-completions"],
    "executor_output_formats": ["chat-completions"]
  }
}
```

**Methods:**

| Method | Purpose |
|---|---|
| `executor.identifier` | Returns the provider identifier this executor handles. |
| `executor.execute` | Executes a non-streaming model request. |
| `executor.execute_stream` | Executes a streaming model request. |
| `executor.count_tokens` | Handles a token-counting request. |
| `executor.http_request` | Entry point for executor-owned HTTP requests. |

### Protocol format declarations (required)

`executor_input_formats` declares which request protocols the executor can accept **directly**.
`executor_output_formats` declares which response protocols it emits **directly**. Common values:
`chat-completions`, `responses`, `anthropic` (the exact string is provider/protocol-specific — the
`claude-web-search-router` self-executor example instead lists `"claude"`).

`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/protocol-format/go/main.go` demonstrates an
executor declaring `chat-completions` input with `responses` output (a translating executor):

```json
{"capabilities":{"executor":true,"executor_model_scope":"both","executor_input_formats":["chat-completions"],"executor_output_formats":["responses"]}}
```

**Development note (hard requirement):** "An executor must declare at least one input format and
one output format."

### `ExecutorModelScope` (static / oauth / both)

```go
// sdk/pluginapi/types.go
type ExecutorModelScope string

const (
    ExecutorModelScopeBoth   ExecutorModelScope = "both"
    ExecutorModelScopeStatic ExecutorModelScope = "static"
    ExecutorModelScopeOAuth  ExecutorModelScope = "oauth"
)
```

| Value | Description |
|---|---|
| `static` | The executor only serves static models. |
| `oauth` | The executor only serves OAuth or credential-bound models. |
| `both` | The executor serves both static and credential-bound models. |

An **empty value is treated as `both`**. This same enum is referenced identically from
`model_provider` (§2) and `model_router`'s self-owned executor (§3, where a router's self-owned
executor was declared with `executor_model_scope: "static"`).

### `ExecutorRequest`

```json
{
  "AuthID": "auth-1",
  "AuthProvider": "plugin-example",
  "Model": "plugin-example-model",
  "Format": "chat-completions",
  "Stream": false,
  "Headers": {},
  "Query": {},
  "OriginalRequest": "base64-client-body",
  "SourceFormat": "chat-completions",
  "Payload": "base64-provider-payload",
  "StorageJSON": "base64-auth-json",
  "AuthMetadata": {},
  "AuthAttributes": {}
}
```

Important: `Payload` arrives **already translated** to the target protocol's request body — "do
not infer the original client protocol again." `StorageJSON` carries the credential: "Credential
data from `StorageJSON` should be used and then discarded" — don't cache/store secrets inside the
plugin longer than needed.

Outbound HTTP from the executor should go through `host.http.*` so logging, proxy settings,
transport policy, and credential context stay host-managed.

### Responses

Non-streaming:

```json
{ "Payload": "base64-response-body", "Headers": { "content-type": ["application/json"] }, "Metadata": {} }
```

Streaming: returns `Headers` plus a chunk stream. "C ABI examples place finite chunks in a response
array, and the host converts them into an internal stream."

**Development notes:**

- Must declare ≥1 input format and ≥1 output format (hard requirement, repeated above).
- `Payload` is pre-translated — don't re-infer protocol.
- **If you just need to reuse the host's own model routing/execution path, don't write an executor
  at all** — use `host.model.*` from host callbacks instead. Executors are for genuinely new
  upstream backends.
- Never store or print upstream secrets; use `StorageJSON` then discard.

**When to use:** Adding a genuinely new upstream/backend that the host doesn't already know how to
talk to — the plugin becomes the actual HTTP adapter. Also used as the "self" target of a
`model_router` plugin (§3) or bound to models from `model_registrar`/`model_provider` (§1–2).

**Vendored examples:**
`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/executor/go/main.go` — the dedicated
single-purpose example, `chat-completions` in/out.
`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/protocol-format/go/main.go` — a translating
executor, `chat-completions` in → `responses` out, to see the input/output format declarations
diverge.

**Source refs:** `sdk/pluginapi/types.go`: `ProviderExecutor`, `ExecutorRequest`,
`ExecutorResponse`, `ExecutorStreamResponse`, `ExecutorHTTPRequest`. `sdk/pluginabi/types.go`:
`executor.identifier`, `executor.execute`, `executor.execute_stream`, `executor.count_tokens`,
`executor.http_request`. `internal/pluginhost/adapters.go`: executor registration, protocol format
selection, execution bridge.

---

## 8. How these capabilities compose together

- **`model_registrar` + `executor`**: if a registrar's static models should be served *only* by
  this plugin's own executor, declare `executor` too and set `executor_model_scope` appropriately.
  Without an executor, registrar-declared models are handled as "normal plugin-provided model
  clients" — implying there's a default/generic execution path when no executor is attached.
- **`model_provider` + `executor`**: `executor_model_scope` on the executor gates whether the
  provider's static path, OAuth path, or both are actually routed to that executor.
- **`model_router` + `executor`**: a router that wants a `self` target must co-declare `executor`,
  and the router's own `TargetKind: "self"`/`"executor"` routes bypass auth-record selection — so
  the target executor must support `executor_model_scope: "static"` or `"both"` since there's no
  credential in play for a directly-routed request.
- **`auth_provider` + `model_provider`**: these "usually work together" — the credential plugin
  supplies `AuthID`/`StorageJSON`, and the model provider's `model.for_auth` uses that to discover
  per-account models.
- **`frontend_auth_provider` + `frontend_auth_provider_exclusive`**: the exclusive flag is
  meaningless without the base capability also being declared.

## 9. Quick-reference: capability flags cheat sheet

```jsonc
// model_registrar
{ "capabilities": { "model_registrar": true } }

// model_provider (optionally scoped by a co-declared executor)
{ "capabilities": { "model_provider": true } }

// model_router (optionally with a self-owned executor)
{
  "capabilities": {
    "model_router": true,
    "executor": true,
    "executor_model_scope": "static",
    "executor_input_formats": ["claude"],
    "executor_output_formats": ["claude"]
  }
}

// auth_provider
{ "capabilities": { "auth_provider": true } }

// frontend_auth_provider (non-exclusive)
{ "capabilities": { "frontend_auth_provider": true } }

// frontend_auth_provider (exclusive)
{ "capabilities": { "frontend_auth_provider": true, "frontend_auth_provider_exclusive": true } }

// executor (standalone upstream adapter)
{
  "capabilities": {
    "executor": true,
    "executor_model_scope": "both",
    "executor_input_formats": ["chat-completions"],
    "executor_output_formats": ["chat-completions"]
  }
}
```

`executor_model_scope` values (used identically by `model_provider`, `model_router`'s self-executor,
and standalone `executor` declarations): `static` | `oauth` | `both` (empty string == `both`).
