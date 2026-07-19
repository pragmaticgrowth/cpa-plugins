# Host Callback Surface (`host.*`)

Host callbacks are the mechanism for a plugin to call back into CLIProxyAPI host capabilities.
They are **not** plugin capability fields — they're always available once a plugin is loaded, and
matter most for executor, Management API, credential, and resource-page plugins. Pinned to
upstream v7.2.88 (`sdk/pluginabi/types.go`, `sdk/pluginapi/types.go`,
`internal/pluginhost/host_callbacks.go`, `docs-plugin/host-callbacks.md`).

Every one of these is invoked identically from the plugin side: marshal the request struct to
JSON, call `host_api->call(host_api->host_ctx, "<method>", bytes, len, &response_buffer)`,
unmarshal the `Envelope`, then free the buffer via `host_api->free_buffer` (never your own
language's `free`/`drop` — see `memory-model.md`).

Example reference plugins: `examples/plugin/host-callback/go/main.go`,
`examples/plugin/host-callback-auth-files/go/main.go`,
`examples/plugin/host-model-callback/go/main.go`.

## HTTP callbacks

| Method | Request type | Response type | Purpose |
|---|---|---|---|
| `host.http.do` | `HTTPRequest{Method, URL string; Headers http.Header; Body []byte}` | `HTTPResponse{StatusCode int; Headers http.Header; Body []byte}` | Non-streaming upstream HTTP call under host transport/proxy/request-log policy. |
| `host.http.do_stream` | `HTTPRequest` | `HTTPStreamResponse{StatusCode int; Headers http.Header; Chunks <-chan HTTPStreamChunk}` (initial) | Streaming upstream HTTP call; returns a stream you then read with `stream_read`. |
| `host.http.stream_read` | stream-id based | `HTTPStreamChunk{Payload []byte; Err error}` | Pull the next chunk of a host-owned HTTP stream. |
| `host.http.stream_close` | stream-id based | — | Close a host-owned HTTP stream. |

Plugins should prefer these methods for external HTTP services so proxy settings, transport
policy, and request logging remain managed by the host. **A plugin author must issue upstream
HTTP calls via `host.http.do`/`host.http.do_stream`, not by dialing out itself** — this is
required for the host's request-log capture to see the real upstream traffic. This is the wire
realization of the conceptual `HostHTTPClient` interface:

```go
type HostHTTPClient interface {
    Do(context.Context, HTTPRequest) (HTTPResponse, error)
    DoStream(context.Context, HTTPRequest) (HTTPStreamResponse, error)
}
```

## Model execution callbacks

| Method | Request type | Response type | Purpose |
|---|---|---|---|
| `host.model.execute` | `HostModelExecutionRequest` | `HostModelExecutionResponse{StatusCode int; Headers http.Header; Body []byte}` | Delegate a full model execution back through the host's own model pipeline. |
| `host.model.execute_stream` | `HostModelExecutionRequest` | `HostModelStreamResponse{StatusCode int \`json:"status_code"\`; Headers http.Header \`json:"headers"\`; StreamID string \`json:"stream_id"\`}` | Streaming variant; returns a `StreamID`. |
| `host.model.stream_read` | `HostModelStreamReadRequest{StreamID string \`json:"stream_id"\`}` | `HostModelStreamReadResponse{Payload []byte \`json:"payload"\`; Error string \`json:"error"\`; Done bool \`json:"done"\`}` | Poll next chunk / terminal state. |
| `host.model.stream_close` | `HostModelStreamCloseRequest{StreamID string \`json:"stream_id"\`}` | — | Close the stream. |

`HostModelExecutionRequest` — all fields carry explicit `snake_case` json tags:

```go
type HostModelExecutionRequest struct {
    EntryProtocol string `json:"entry_protocol"`  // inbound client protocol
    ExitProtocol  string `json:"exit_protocol"`   // target provider protocol
    Model  string `json:"model"`
    Stream bool   `json:"stream"`
    Body   []byte `json:"body"`
    Headers http.Header `json:"headers"`
    Query   url.Values  `json:"query"`
    Alt     string `json:"alt"`
}
```

Example core request fields (from `docs-plugin/host-callbacks.md`):

```json
{
  "entry_protocol": "openai",
  "exit_protocol": "openai",
  "model": "gpt-5.5",
  "stream": false,
  "body": "base64-request-body",
  "headers": {},
  "query": {},
  "alt": ""
}
```

These let a plugin (e.g. a router or executor) delegate a request straight back through the
host's own model pipeline instead of doing its own transformation — useful for
`ModelRouteTargetProvider`. Prefer `host.model.*` over copying host credentials into the plugin
whenever the host model execution path can be reused.

### `host_callback_id` and non-recursion

Many request structs carry a `HTTPClient HostHTTPClient \`json:"-"\`` field that's excluded from
wire JSON — in the real cross-boundary RPC, host-side wrapper types
(`internal/pluginhost/rpc_schema.go`, e.g. `rpcAuthLoginStartRequest`, `rpcExecutorRequest`,
`rpcExecutorHTTPRequest`) instead add a `HostCallbackID string \`json:"host_callback_id,omitempty"\``
field so the plugin can reconstruct an `HTTPClient`-equivalent by calling back
`host.http.do`/`do_stream` using that opaque ID as context.

When a plugin calls `host.model.*` from a host-invoked context such as `management.handle`, it
should **forward the request's `host_callback_id`**. The host uses this ID to identify the plugin
that originated the callback. During nested model execution, it skips that plugin's own request,
response, and stream interceptors to avoid recursion — other enabled plugins may still handle the
nested request. This is the mechanism that prevents a plugin's own interceptor from re-firing on
a model execution the plugin itself triggered.

## Stream bridge and logging

| Method | Request type | Response type | Purpose |
|---|---|---|---|
| `host.stream.emit` | plugin-defined chunk | — | Plugin pushes a chunk into a host-managed outbound stream (used by executor streaming replies). |
| `host.stream.close` | stream-id based | — | Plugin closes a host-managed outbound stream. |
| `host.log` | log message payload | — | Plugin writes into host-managed structured logs. |

Explicitly call the matching close method after using a streaming callback — the host does not
auto-close streams on your behalf.

## Credential file callbacks (`host.auth.*`)

| Method | Request type | Response type | Purpose |
|---|---|---|---|
| `host.auth.list` | — | `[]HostAuthFileEntry` | List credentials visible to the host. |
| `host.auth.get` | `HostAuthGetRequest{AuthIndex string \`json:"auth_index"\`}` | `HostAuthGetResponse{AuthIndex, Name, Path string; JSON json.RawMessage \`json:"json"\`}` | Fetch raw credential JSON by auth index. |
| `host.auth.get_runtime` | `HostAuthGetRequest`-style | `HostAuthGetRuntimeResponse{Auth HostAuthFileEntry \`json:"auth"\`}` | Fetch full runtime credential state. |
| `host.auth.save` | `HostAuthSaveRequest{Name string \`json:"name"\`; JSON json.RawMessage \`json:"json"\`}` (`Name` must end `.json`) | `HostAuthSaveResponse{Name, Path string}` | Persist credential JSON to a physical auth file; updates the runtime credential record. |

`HostAuthFileEntry` — one credential's full public-facing state (all fields `snake_case`-tagged,
most `omitempty`):

```go
type HostAuthFileEntry struct {
    ID, AuthIndex, Name, Type, Provider, Label, Status, StatusMessage string
    Disabled, Unavailable, RuntimeOnly bool
    Source, Path string; Size int64
    ModTime, UpdatedAt, CreatedAt, LastRefresh, NextRetryAfter time.Time
    Email, ProjectID, AccountType, Account string
    Priority int; Note string; Websockets bool
    Success, Failed int64
    RecentRequests []HostRecentRequestEntry
}
type HostRecentRequestEntry struct { Time string; Success, Failed int64 }
```

`examples/plugin/host-callback-auth-files` shows how to call these methods from a resource page.

## Development notes (security-relevant)

- Explicitly call the matching close method after using a streaming callback.
- Do not use host callbacks to bypass the plugin's own security boundary; a plugin is still
  trusted in-process code (see the ABI trust-boundary note in `abi-contract.md` §11).
- Do not write credential JSON, tokens, or user request bodies to logs (`host.log`).
- Do not expose credential-reading, credential-writing, or other privileged host callbacks
  directly through unauthenticated resource GET query parameters (`ResourceRoute` handlers are
  **not** management-authenticated). If a resource page needs a user-facing control for those
  actions, let its same-origin JavaScript read trusted Management Center storage and call an
  authenticated `/v0/management/...` route instead.
- When the host model execution path can be reused, prefer `host.model.*` instead of copying host
  credentials into the plugin.

## Embedder-side context (not part of the plugin-author contract)

`sdk/pluginhost.Host` (public Go wrapper, `sdk/pluginhost/host.go`) is the **embedder-side**
mirror of this callback surface — what a Go program hosting the plugin runtime (not a plugin)
uses to drive plugins in-process: `New()`, `ApplyConfig(ctx, RuntimeConfig)`,
`ParseAuth`/`ParseAuths`, `ModelsForAuth`/`ModelsForProvider`, `RefreshAuth`, `HasAuthProvider`,
`StartLogin`/`PollLogin`, `AuthDataToCoreAuth`, `PickAuth`, `HasScheduler`,
`RegisteredPlugins()`, `ShutdownAll()`, `PluginBusy(id)`, `UnloadPlugin(id)`. Skip this section if
you're just writing a plugin — it's the API a *host binary* embeds, not something a plugin calls.

```go
type RuntimeConfig struct {
    Enabled bool; Dir string; AuthDir string; ProxyURL string; ForceModelPrefix bool
    OAuthModelAlias     map[string][]OAuthModelAlias
    OAuthExcludedModels map[string][]string
    Configs             map[string]PluginInstanceConfig
}
type PluginInstanceConfig struct { Enabled *bool; Priority int; Raw yaml.Node }
type OAuthModelAlias struct { Name, Alias string; Fork bool }
```
