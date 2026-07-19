# OpenCode Go — CLIProxyAPI native provider plugin (`opencode-go`)

**Status:** approved design (brainstorming), pending spec review → implementation plan
**Date:** 2026-07-19
**Author:** Serkan Haşlak (with Claude Code)
**Target host:** CLIProxyAPI v7.2.88 (`github.com/router-for-me/CLIProxyAPI/v7`)

## 1. Goal

Add **OpenCode Go** — the low-cost coding-models subscription at `https://opencode.ai/zen/go/v1` —
to CLIProxyAPI as a first-class provider, so its models are reachable through CLIProxyAPI's
OpenAI-/Claude-/Gemini-compatible surfaces. Delivered as a **native `cpa` plugin** (a c-shared
`.dylib`/`.so`/`.dll`), not a config-only wiring, so the provider is installable/redistributable
and its model catalogue is discovered live rather than hand-maintained.

## 2. Background — verified ground truth

All facts below were verified against live probes with a real key and against upstream source
(anomalyco/opencode `go.mdx`, `models.dev` registry, CLIProxyAPI v7.2.88 source).

- **Endpoint (base URL):** `https://opencode.ai/zen/go/v1` (models.dev provider id `opencode-go`).
- **Auth:** a plain pre-shared API key, format `sk-` + 64 alphanumerics. Sent as
  `Authorization: Bearer <key>`. **No OAuth, no refresh, no expiry.** The key self-scopes to a
  workspace server-side; no workspace/org header is needed. It is the *same* key as OpenCode Zen —
  "Go" is a billing flag that gates the cheaper "lite" model list.
- **Wire format — universal OpenAI-compatible:** the gateway exposes `POST /chat/completions`
  (OpenAI), `POST /messages` (Anthropic), and `GET /models`. **Critically, `/chat/completions`
  accepts _all_ 22 models** — including the minimax/qwen models that natively prefer `/messages` —
  and returns clean OpenAI `chat.completion` shape (verified: `minimax-m3` → HTTP 200 on
  `/chat/completions`). So the whole catalogue is reachable through one OpenAI-compatible endpoint.
- **Model catalogue (live `GET /models`, 22 ids):** `grok-4.5`, `glm-5.2`, `glm-5.1`, `glm-5`,
  `kimi-k3`, `kimi-k2.7-code`, `kimi-k2.6`, `kimi-k2.5`, `deepseek-v4-pro`, `deepseek-v4-flash`,
  `minimax-m3`, `minimax-m2.7`, `minimax-m2.5`, `qwen3.7-max`, `qwen3.7-plus`, `qwen3.6-plus`,
  `qwen3.5-plus`, `mimo-v2-pro`, `mimo-v2-omni`, `mimo-v2.5-pro`, `mimo-v2.5`, `hy3-preview`.
  The catalogue changes over time (models added/deprecated), which is the motivation for live
  discovery.
- **Usage limits:** dollar-denominated rolling (5h $12) / weekly ($30) / monthly ($60) windows,
  enforced server-side. Not modelled by this plugin in v1 (see §9 non-goals).

## 3. Key design decision — capabilities

**The plugin implements exactly two capabilities: `auth_provider` + `model_provider`. It does NOT
implement `executor`, translators, or normalizers.**

Rationale (verified in CLIProxyAPI v7.2.88 source):
- CLIProxyAPI ships a generic built-in `OpenAICompatExecutor`
  (`internal/runtime/executor/openai_compat_executor.go`) that **auto-binds to any credential whose
  provider key is not claimed by a plugin's own executor** (`sdk/cliproxy/service.go`
  `registerExecutorForAuth` default branch → `HasExecutorCandidateProvider` is false for us →
  host registers `NewOpenAICompatExecutor(providerKey, cfg)`).
- That executor reads the base URL and key straight off the credential's `Attributes` map at
  request time (`resolveCredentials`: `Attributes["base_url"]`, `Attributes["api_key"]`) and always
  sends `Authorization: Bearer <api_key>` — exactly OpenCode Go's contract. It already implements
  request/response translation, SSE streaming, token counting, and usage reporting.
- Therefore the plugin's entire job is: **(a) mint a credential** carrying
  `Attributes{base_url, api_key}`, and **(b) list the models**. Declaring an executor would make the
  host defer to us and force us to re-implement HTTP/SSE for zero benefit.

## 4. Component: `auth_provider` (provider key `opencode-go`)

Implements `pluginapi.AuthProvider` over the ABI methods `auth.identifier`, `auth.parse`,
`auth.login.start`, `auth.login.poll`, `auth.refresh`.

### 4.1 `auth.identifier`
Returns `{"identifier":"opencode-go"}`.

### 4.2 `auth.parse` — primary credential mechanism (credential file)
The host scans its auth dir (`HostConfigSummary.AuthDir`, default `~/.cli-proxy-api`) and offers each
JSON file to plugin parsers via `AuthParseRequest{Provider, Path, FileName, RawJSON, Host}`. The
plugin recognises an OpenCode Go credential file by a `type` marker and returns
`AuthParseResponse{Handled, Auth}`.

**Credential file** — `<AuthDir>/opencode-go.json` (the key is supplied by the operator; never
committed):
```json
{ "type": "opencode-go", "api_key": "sk-...", "base_url": "https://opencode.ai/zen/go/v1" }
```
`base_url` is optional in the file; if absent the plugin fills the default (or the config override,
§6). `ParseAuth` returns `Handled=false` for any file lacking `"type":"opencode-go"` so it never
steals another provider's file.

**Returned `AuthData`:**
| Field | Value |
|---|---|
| `Provider` | `opencode-go` |
| `ID` | `opencode-go` (stable; single credential per file) |
| `FileName` | `opencode-go.json` |
| `Label` | `OpenCode Go` |
| `Prefix` | `""` (raw model ids — see §7) |
| `StorageJSON` | the persisted credential bytes (`{type,api_key,base_url}`) — plugin-owned |
| `Attributes` | `{"base_url":"https://opencode.ai/zen/go/v1","api_key":"sk-..."}` — **read by the built-in executor** |
| `Metadata` | `{"type":"opencode-go"}` |
| `NextRefreshAfter` | zero value (no scheduled refresh) |

### 4.3 `auth.refresh`
The key never expires. `RefreshAuth` returns the input `Auth` unchanged with a far-future / zero
`NextRefreshAfter` — a safe no-op that keeps the credential valid.

### 4.4 `auth.login.start` / `auth.login.poll`
The host login flow is modelled as URL + opaque state + poll (designed for OAuth/device-code). A
pre-shared pasted key does not fit that shape. **v1 does not implement an interactive login**;
these methods return an `error`/`Status:"error"` envelope with a message directing the operator to
the credential-file path (§4.2). (A `command_line_plugin` helper that writes the credential file
from `$OPENCODE_API_KEY` is a documented stretch — §10.)

## 5. Component: `model_provider` (provider key `opencode-go`)

Implements `pluginapi.ModelProvider` over ABI methods `model.static`, `model.for_auth`. The
returned `ModelResponse.Provider` MUST equal the `auth_provider` key (`opencode-go`, lowercased) so
discovered ids route back to the same credential + built-in executor.

### 5.1 `model.for_auth` — live discovery (primary)
Receives `AuthModelRequest{AuthID, AuthProvider, StorageJSON, Attributes, Host, HTTPClient}`.
Using the host HTTP bridge (`host.http.do`, so proxy/logging policy is honoured), issue
`GET https://opencode.ai/zen/go/v1/models` with `Authorization: Bearer <api_key>` (key from
`Attributes["api_key"]` / `StorageJSON`). Map the OpenAI-list response
(`{"data":[{"id","object","owned_by",...}]}`) into `ModelResponse{Provider:"opencode-go", Models:[...]}`.

Each `ModelInfo`: `ID` = upstream id, `Object` = `model`, `OwnedBy` = `opencode-go`,
`DisplayName` = a title-cased label, `SupportedGenerationMethods` = `["chat"]`, `UserDefined` = true.
(Context/output limits from models.dev are a nice-to-have; minimal fields are sufficient for
routing.)

**Resilience:** if the live fetch fails, return the **static list** (§5.2) rather than an error, so
the models still appear. (Upstream note: returning an error marks discovery "handled but failed"
for that credential.)

### 5.2 `model.static` — baked-in fallback
Returns the same `ModelResponse` shape with a hard-coded list of the 22 known ids. Guarantees the
provider is usable even before/without a live `/models` call, and covers discovery-fetch failures.

## 6. Configuration (`plugins.configs.opencode-go`)

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    opencode-go:
      enabled: true            # host-reserved
      priority: 1              # host-reserved
      base_url: "https://opencode.ai/zen/go/v1"   # plugin ConfigField; default used if omitted
      discovery: "live"        # "live" (default) prefer GET /models; "static" use baked-in list only
```
Declared `ConfigField`s: `base_url` (string), `discovery` (string enum). `enabled`/`priority` are
host-reserved. Config is parsed in **both** `plugin.register` and `plugin.reconfigure`.

## 7. Model naming

Expose **raw upstream ids** (`kimi-k3`, `grok-4.5`, `deepseek-v4-flash`, …) with `Prefix=""`.
Clients call `POST /v1/chat/completions` with `model: "kimi-k3"`. Collision risk exists only if
another configured provider serves an identically-named id; acceptable for v1, and mitigable later
by setting `AuthData.Prefix` (e.g. `opencode-go`) without touching the wire logic.

## 8. Request-time data flow

```
client → CLIProxyAPI (/v1/chat/completions | /v1/messages | /v1beta/... )
      → ModelRouter resolves model id → provider "opencode-go"
      → scheduler picks the opencode-go credential (from auth_provider)
      → built-in OpenAICompatExecutor reads Attributes{base_url, api_key}
      → POST https://opencode.ai/zen/go/v1/chat/completions  (Authorization: Bearer)
      → response translated back to the client's entry protocol
```
All 22 Go models become reachable on every CLIProxyAPI entry surface, with no plugin code on the
request path.

## 9. Repository layout & convention

Per owner decision (2026-07-19): **real provider plugins live inside this repo under `projects/`**
(reversing the prior "consuming projects only" convention). This is the first such plugin.

```
projects/opencode-go/
  main.go                 # cgo c-shared plugin: init + call/free/shutdown, handleMethod dispatch
  go.mod                  # module + require github.com/router-for-me/CLIProxyAPI/v7 v7.2.88
  Makefile                # CGO_ENABLED=1 go build -buildmode=c-shared -o opencode-go.dylib .
  README.md               # install/wire/credential/test instructions
  config.snippet.yaml     # the plugins.configs.opencode-go block (§6)
  opencode-go.json.example# credential-file template (placeholder key, not a real one)
docs/superpowers/specs/2026-07-19-opencode-go-provider-design.md   # this spec
```
Also update `/Users/serkan/cpa-plugins/CLAUDE.md` "What this repo is (and isn't)" to document the
`projects/` convention.

## 10. Non-goals (v1)

- **No `executor`, no translators/normalizers** — the built-in OpenAI-compatible executor handles
  the wire (§3).
- **No `usage_plugin`** — rolling/weekly/monthly limit tracking from the response `cost` field is a
  future enhancement.
- **No OAuth/device-code login** — the key is pre-shared; interactive login (`auth.login.*`) is
  stubbed (§4.4). A CLI credential-writer helper (`command_line_plugin`) is a stretch, not v1.
- **No OpenCode Zen** (`/zen/v1`) — only Go (`/zen/go/v1`). The same design generalises to Zen but
  Zen's premium models split across `/messages` and `/responses`; out of scope here.
- **No management UI.**

## 11. Testing plan

1. **Build:** `CGO_ENABLED=1 go build -buildmode=c-shared -o opencode-go.dylib .`;
   `nm -gU opencode-go.dylib | grep cliproxy_plugin_init`.
2. **Validate:** `/cpa:validate` — ABI exports, ABI version = 1, required metadata present.
3. **Install & wire:** copy to `<plugins.dir>/<GOOS>/<GOARCH>/opencode-go.dylib`; add the §6 config
   block; create `<AuthDir>/opencode-go.json` with the key sourced from `$OPENCODE_API_KEY` (never
   committed).
4. **Registration:** start the host; `GET /v0/management/plugins` shows `opencode-go` with
   `auth_provider` + `model_provider`.
5. **Model discovery:** `GET /v1/models` lists the 22 `opencode-go` models.
6. **End-to-end (OpenAI-family):** `POST /v1/chat/completions` `model=deepseek-v4-flash` → HTTP 200
   completion routed to the live gateway.
7. **End-to-end (Anthropic-family via universal endpoint):** `model=minimax-m3` on
   `/v1/chat/completions` → HTTP 200 (confirms the built-in executor + universal endpoint cover the
   whole catalogue).
8. **Fallback:** with `discovery: static` (or the network blocked), models still list from the
   baked-in set.

## 12. Risks / to confirm during implementation

- **Credential-file discovery:** confirm the host scans `AuthDir` for `*.json` and offers each to
  `auth.parse` (doc says so; verify the `type`-marker gating behaves and no double-registration).
- **Provider-key ↔ model routing:** confirm `ModelResponse.Provider` (lowercased) must equal
  `AuthData.Provider` for ids to route to the built-in executor (researcher-confirmed; validate live).
- **Executor rebind timing:** the executor→credential rebind runs on plugin load / config reload /
  startup sweep; confirm the credential + models appear after host start without a manual reload
  (researcher flagged single-call rebind as not fully traced).
- **`host.http.do` envelope:** confirm the request/response JSON shape for the HTTP bridge from
  `sdk/pluginabi` + the `examples/host-callback` example before wiring the `/models` fetch.
- **Model metadata depth:** start with minimal `ModelInfo` fields; enrich from models.dev only if a
  client needs context-length/limits.

## 13. Open sub-decisions deferred to the plan

- Whether to enrich `ModelInfo` with models.dev context/limit metadata in v1 or ship minimal.
- Whether to add the optional `command_line_plugin` credential-writer helper in v1 or defer.
