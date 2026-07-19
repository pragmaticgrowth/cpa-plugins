# Warp — CLIProxyAPI native provider plugin (`warp`)

Status: **approved design** (2026-07-19). Feeds the implementation plan.
Location: `projects/warp/` (in-repo, per the 2026-07-19 owner decision; mirrors
`projects/opencode-go/`).

## 1. Goal

Make **Warp AI** a first-class provider inside CLIProxyAPI so a user can spend
their **Warp subscription credits** through any entry protocol the host exposes
(OpenAI `/v1/chat/completions`, Anthropic `/v1/messages`, Gemini, …). Ship a
single native Go plugin `warp.dylib`/`.so`/`.dll` built from the cpa toolchain.

The user's literal ask: "use its credits via CLIProxyAPI, bring Warp as a
provider." The credits switch is `Settings.ApiKeys.allow_use_of_warp_credits =
true` with no BYOK keys set (see §7).

## 2. Background — verified ground truth

Confirmed by cross-checking Warp's **open-sourced client** (`warpdotdev/warp`,
Rust, AGPL-3.0), its **published protobuf schema** (`warpdotdev/warp-proto-apis`,
AGPL-3.0, ships pre-generated Go bindings), and two independent reverse-engineered
bridges (`Xchat1/Warp2Api`, `loongee/warp-local-proxy`).

- **No static API key exists.** Auth is Firebase / Google Identity Platform:
  a long-lived **refresh token** → short-lived **access-token JWT** (~1h).
  Client stores it in macOS Keychain, service `dev.warp.Warp-Stable`.
- **Refresh:** `POST https://app.warp.dev/proxy/token?key=<firebase_web_key>`,
  `content-type: application/x-www-form-urlencoded`,
  body `grant_type=refresh_token&refresh_token=<t>`. Response JSON: only
  `access_token` is load-bearing (decode permissively). Firebase web key
  (public, ships in every client): `AIzaSyBdy3O3S9hrdayLJxJ7mriBR4qgUaUygAs`.
- **AI endpoint:** `POST https://app.warp.dev/ai/multi-agent`, **HTTP/2**,
  `content-type: application/x-protobuf`, `accept: text/event-stream`,
  `authorization: Bearer <jwt>`, plus `x-warp-client-version` and
  `x-warp-os-{category,name,version}`. Request body = serialized
  `warp.multi_agent.v1.Request`. Response = SSE; each `data:` line is a
  base64url-encoded (hex fallback) serialized `warp.multi_agent.v1.ResponseEvent`,
  terminated by `data: [DONE]`.
- **Model IDs** are plain strings (`claude-4.1-opus`, `gpt-5`, `gemini-2.5-pro`,
  `auto`, …), server-driven catalogue, no translation table needed.
- **Proto is Editions-2023 + Opaque API** → generated Go uses
  builder/setter accessors, not field assignment.

## 3. Legal / licensing (accepted)

- Unofficial, unsupported: impersonates the desktop client to consume a
  subscription. No sanctioned Warp API. Owner accepts this tradeoff.
- Importing Warp's AGPL-3.0 Go bindings makes this plugin AGPL-3.0. Accepted for
  in-repo/personal use. A future permissive release would regenerate structs from
  the `.proto` under our own terms; out of scope for v1. Record the AGPL notice in
  `projects/warp/README.md`.

## 4. Architecture

Mirror `projects/opencode-go/` exactly:

- `main.go` — **thin C-ABI adapter only**: the four `//export` functions
  (`cliproxy_plugin_init`, `cliproxyPluginCall`, `cliproxyPluginFree`,
  `cliproxyPluginShutdown`), the host-callback bridge (`callHost`), and — new vs.
  opencode-go — exposing the host caller to `internal/core` so the streaming
  executor can invoke `host.stream.emit` / `host.stream.close` from a goroutine.
  All logic delegates to `core.Dispatch(method, requestBytes, hostCaller)`.
- `internal/core/` — all logic, unit-testable (`go test ./internal/core/...`):
  - `dispatch.go` — method-name switch → capability handlers, `{ok,result,error}`
    envelope wrapping.
  - `register.go` — `plugin.register`/`plugin.reconfigure` → capabilities map +
    metadata; config parse (`config_yaml`, YAML) cached in an `atomic.Value`.
  - `auth.go` — `auth.identifier`, `auth.parse`, `auth.refresh`,
    `auth.login.start`, `auth.login.poll`.
  - `models.go` — `model.register` static catalogue.
  - `cli.go` — `command_line.register` (`--warp-login`) + `command_line.execute`
    (Keychain import / paste, initial refresh, returns `Auths`).
  - `executor.go` — `executor.identifier/execute/execute_stream/count_tokens/
    http_request`.
  - `warpreq.go` — chat-completions JSON → `warp.multi_agent.v1.Request`.
  - `warpresp.go` — `ResponseEvent` SSE stream → chat-completions (stream + full).
  - `token.go` — Firebase refresh-token exchange (over the host HTTP bridge).
  - `credential.go` — the `StorageJSON` shape + JWT expiry parsing.
- `go.mod` — module `github.com/pragmaticgrowth/cpa-plugins/projects/warp`,
  `go 1.26.0`, requires:
  - `github.com/router-for-me/CLIProxyAPI/v7 v7.2.88` (SDK types)
  - `github.com/warpdotdev/warp-proto-apis/apis/multi_agent/v1/gen/go` (bindings)
  - `google.golang.org/protobuf` (runtime for the opaque API)
- `Makefile`, `config.snippet.yaml`, `warp.json.example`, `.gitignore`, `README.md`
  — same shape as opencode-go.

Host callbacks used: `host.http.do` (all outbound calls — refresh + AI), and for
streaming `host.stream.emit` / `host.stream.close`. Never open raw sockets; go
through the host so proxy/logging policy applies.

## 5. Capabilities & registration

`plugin.register` / `plugin.reconfigure` return (PascalCase nested metadata; the
capability keys are the documented wire keys):

```json
{
  "schema_version": 1,
  "metadata": { "Name": "warp", "Version": "0.1.0", "Author": "...",
                "GitHubRepository": "...", "ConfigFields": [ ... ] },
  "capabilities": {
    "auth_provider": true,
    "model_registrar": true,
    "executor": true,
    "executor_model_scope": "oauth",
    "executor_input_formats": ["chat-completions"],
    "executor_output_formats": ["chat-completions"],
    "command_line_plugin": true
  }
}
```

**The one-format decision:** declaring `chat-completions` in *and* out means the
host's built-in translators adapt every other client protocol to/from it, so the
plugin only maps `chat-completions ⟷ Warp protobuf`. Scope `oauth` routes the
credential-bound models to our executor.

## 6. Credential model & auth flows

Provider key: `warp`. Auth file: `<AuthDir>/warp-<label>.json` (AuthDir default
`~/.cli-proxy-api`). Plugin-owned `StorageJSON` blob:

```json
{ "type": "warp", "refresh_token": "<firebase refresh token>",
  "access_token": "<jwt>", "expires_at": "2026-07-19T14:00:00Z",
  "email": "you@example.com" }
```

### 6.1 `auth.identifier`
Returns `"warp"`.

### 6.2 `auth.parse`
Given raw file bytes, accept iff `type == "warp"` and a `refresh_token` is
present. Return `AuthData{Provider:"warp", ID, FileName, Label, StorageJSON,
NextRefreshAfter}`. `NextRefreshAfter` = `expires_at` minus 5 min (or "now" if no
cached access token / already expired).

### 6.3 `auth.refresh`
Host hands back the current `StorageJSON` + `AuthID`. Call the Firebase exchange
(`token.go`) via the injected host HTTP client, replace `access_token`/`expires_at`
in `StorageJSON`, return `AuthRefreshResponse{Auth, NextRefreshAfter}` with
`NextRefreshAfter` = new expiry − 5 min (absolute RFC3339 timestamp). The host
schedules and drives this; the executor consumes the already-fresh token.

### 6.4 `auth.login.start` / `auth.login.poll`
v1: minimal. `login.start` returns a `Message`-style pointer telling the user to
run `--warp-login`; `login.poll` returns `success` if a Keychain credential is now
importable, else `pending`/`error`. (The real acquisition lives in the CLI command,
§8 — the documented pattern for credential import.)

## 7. Executor — request/response mapping

`ExecutorRequest` delivers `Model`, `Format:"chat-completions"`, `Payload`
(already-translated chat-completions JSON), `StorageJSON` (our fresh creds),
`Stream`, and an injected host HTTP client.

### 7.1 chat-completions → `warp.multi_agent.v1.Request`
- `model` → strip `model_prefix` (§9) → `Settings.ModelConfig.base`.
- **Credits:** `Settings.ApiKeys.allow_use_of_warp_credits = true`; leave
  `anthropic/openai/google/open_router` empty (unless a future BYOK mode).
- Messages, stateless (send full history inline every call; let the server
  allocate a fresh `conversation_id`):
  - All messages **except the last** → `TaskContext.tasks[0].messages[]`:
    `user` → `Message.user_query.query`; `assistant` → `Message.agent_output.text`.
    Assign each a synthetic `id` and shared `task_id`.
  - **Last user turn** → `Input.user_inputs.inputs[0].user_query.query`.
  - `system` messages → concatenate → `UserQuery.referenced_attachments`
    map key `"SYSTEM_PROMPT"` → `Attachment.plain_text` on the final query. (No
    native system field; this is the established convention — do NOT copy
    Warp2Api's `<ALERT>` preamble.)
- Minimal required set: `input.user_inputs.inputs[0].user_query.query` +
  `settings.model_config.base`. Capability booleans on `Settings` default false
  for a text-only v1 (`supported_tools` empty; tool arms are v2).
- Do **not** rely on `TaskContext.active_task_id` (reserved in the current proto).

### 7.2 `ResponseEvent` SSE → chat-completions
Decode each `data:` line (try base64url w/ padding, then hex). For each
`ResponseEvent`:
- `init` → capture `conversation_id`/`request_id` (informational; not persisted
  across proxy requests in v1).
- `client_actions.actions[]`: extract assistant text from either
  `append_to_message_content.message.agent_output.text` or
  `add_messages_to_task.messages[].agent_output.text` → emit as `delta.content`.
- `finished` → finish reason mapping: `done` → `stop`; `max_token_limit` →
  `length`; `quota_limit`/`invalid_api_key`/`internal_error`/`llm_unavailable` →
  surfaced as errors (§10). Usage from `conversation_usage_metadata` /
  `request_cost` when present (`token_usage` is `internal`, often empty).
- `[DONE]` sentinel ends the stream.

### 7.3 execute vs execute_stream
- **execute** (non-stream): drive the SSE to completion, accumulate text, return
  one chat-completions response JSON in `ExecutorResponse.Payload`.
- **execute_stream** (Mechanism 2, confirmed for dynamic plugins): return headers
  immediately (`content-type: text/event-stream`), then a goroutine reads Warp's
  SSE and pushes chat-completions SSE chunks via `host.stream.emit(stream_id,…)`,
  finishing with `host.stream.close(stream_id, err)`. `stream_id` arrives as a
  wire-only field on the execute_stream request; recover panics → `close` with the
  error (never let a panic escape — it fuses the plugin).
- **count_tokens / http_request**: minimal/no-op for v1 (return a best-effort
  estimate or empty), documented as v2.

### 7.4 Transport
HTTP/2 via the host HTTP client. Headers per §2, with `x-warp-client-version` and
`x-warp-os-*` sourced from config (overridable — the staleness risk). Body =
serialized protobuf bytes.

## 8. `command_line_plugin` — `--warp-login`

Register a `bool` flag `warp-login` (+ a `string` flag `warp-refresh-token` for
paste). `command_line.execute`:
1. If `--warp-refresh-token` given, use it. Else read
   `security find-generic-password -s dev.warp.Warp-Stable -w`, parse the JSON,
   extract `refresh_token`.
2. Do one refresh (`token.go`) to obtain the initial `access_token`/`expires_at`.
3. Build the `StorageJSON` and return it in
   `CommandLineExecutionResponse.Auths` (host persists as the auth file). Print a
   confirmation to stdout.

This satisfies "browser login" pragmatically: the user logs into the Warp *app*
in their browser as normal; we import what it stored. A standalone browser-OAuth
capture of the `warp://` redirect is out of scope (fragile from a separate binary).

## 9. Models & naming

`model.register` returns a curated static `ModelInfo` list, `OwnedBy:"warp"`,
scope `oauth`. Seed set (adjust during implementation against the live picker):
`claude-4.1-opus`, `claude-4-opus`, `claude-4-sonnet`, `claude-4.5-sonnet`,
`gpt-5`, `gpt-5 (high reasoning)`, `gpt-4.1`, `gpt-4o`, `o3`, `gemini-2.5-pro`,
`auto`.

**Namespacing:** register IDs with a configurable prefix (`model_prefix`, default
`warp/`) → `warp/gemini-2.5-pro`, so they never collide with the native Gemini
provider's `gemini-2.5-pro`. The executor strips the prefix before setting
`ModelConfig.base`. (A later `model_provider` could fetch the live catalogue via
`app.warp.dev/graphql/v2`; static is fine for v1.)

## 10. Configuration (`plugins.configs.warp`)

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    warp:
      enabled: true
      priority: 10
      use_warp_credits: true                              # -> allow_use_of_warp_credits
      model_prefix: "warp/"
      client_version: "v0.2025.08.06.08.12.stable_02"     # override when Warp bumps it
      os_category: "Windows"                              # x-warp-os-* triad
      os_name: "Windows"
      os_version: "11 (26100)"
```

Parsed in **both** register and reconfigure; stored in an `atomic.Value`.

## 11. Error / quota handling

- HTTP 429 with body `"No remaining quota"` / `"No AI requests remaining"`, and
  `StreamFinished.quota_limit` → return a clear upstream 429 to the client.
- Refresh failure `INVALID_REFRESH_TOKEN` / `TOKEN_EXPIRED` → auth error so the
  host prompts re-login (`--warp-login`).
- `internal_error` / `llm_unavailable` / `invalid_api_key` → mapped upstream error
  envelope. Never panic across the ABI (fuses the plugin) — always return errors.

## 12. Build / install / verify

- Build: `make build` (`CGO_ENABLED=1 go build -buildmode=c-shared -o warp.dylib .`).
- ABI check: `nm -gU warp.dylib | grep cliproxy_plugin_init`.
- Install into a host's `plugins/<GOOS>/<GOARCH>/` (or `plugins/`), wire
  `config.snippet.yaml`, restart.
- Registration check: `GET /v0/management/plugins` shows `warp` with the four
  capabilities.
- Functional: `--warp-login`; then curl `/v1/chat/completions`
  `{"model":"warp/claude-4-sonnet",...}` (non-stream then `stream:true`); confirm a
  real response and that Warp credits decremented.

## 13. Staged implementation phases

1. **Skeleton** — ABI adapter + `core.Dispatch` + register 4 caps + config parse.
   Builds; ABI export present; `/v0/management/plugins` lists it.
2. **Auth** — `credential.go`, `token.go`, `auth.parse/refresh`, `--warp-login`.
   Verify a live refresh returns a usable JWT.
3. **Models** — static catalogue with prefix; appears in `/v1/models`.
4. **Executor non-stream** — `warpreq.go` + `warpresp.go` (accumulate) + `execute`.
   First real credits-spending completion.
5. **Executor stream** — `execute_stream` via host.stream.emit/close.
6. **Polish** — full history mapping, system-prompt folding, error/quota mapping,
   config-driven headers, README (incl. AGPL notice).
7. **v2 (later, out of scope)** — tool/function calling (`ToolCall`/`ToolCallResult`
   arms), live model catalogue via GraphQL, usage_plugin reporting.

## 14. Non-goals (v1)

Tool/function calling; image/multimodal input; MCP; multi-turn `conversation_id`
persistence across proxy requests; anonymous free-quota accounts; a live GraphQL
model catalogue; `count_tokens` accuracy.

## 15. Risks / to confirm during implementation

- **Editions-2023 / opaque-API codegen:** confirm `go get` of the pre-generated
  `gen/go` module builds under CGO c-shared with `google.golang.org/protobuf`
  v1.36.6+; else vendor the `.pb.go` or regenerate with a new enough
  `protoc-gen-go`.
- **`client_version` staleness:** Warp may reject stale client-version strings —
  config-overridable; document how to refresh it.
- **SSE payload encoding** observed to vary (base64url/hex) — decode defensively.
- **`/proxy/token` response shape** beyond `access_token` is inferred (Google
  Secure Token convention) — decode permissively.
- **Keychain read** requires the Warp app installed + logged in on macOS; paste
  fallback covers the rest. Linux/Windows credential-store paths are v2.
- **HTTP/2** appears required in practice — default to h2, treat h1.1 as risky.

## 16. Primary sources

- `warpdotdev/warp` (Rust client, auth + endpoint constants) — AGPL-3.0.
- `warpdotdev/warp-proto-apis` — `apis/multi_agent/v1/{request,response,task,
  attachment}.proto` + `gen/go` bindings — AGPL-3.0.
- `Xchat1/Warp2Api` — working reference impl (auth sequencing, header values,
  request/response mapping).
- CLIProxyAPI plugin contracts: `plugins/cpa/references/upstream/`
  (`pluginapi-types.go`, `pluginabi-types.go`, `docs-plugin/*`, `examples/*`).
- Structural precedent: `projects/opencode-go/`.
