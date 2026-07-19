# RPC Method Catalog

All constants below are defined once, in `sdk/pluginabi/types.go`. A plugin's `call(method, ...)`
dispatcher switches on these exact string values. All are **host → plugin** calls unless marked
"host callback" (plugin → host, §7). Request/response type names refer to `sdk/pluginapi/types.go`
unless noted. Pinned to upstream v7.2.88.

```go
const (
	MethodPluginRegister    = "plugin.register"
	MethodPluginReconfigure = "plugin.reconfigure"
	MethodPluginShutdown    = "plugin.shutdown"

	MethodModelRegister = "model.register"
	MethodModelStatic   = "model.static"
	MethodModelForAuth  = "model.for_auth"

	MethodAuthIdentifier = "auth.identifier"
	MethodAuthParse      = "auth.parse"
	MethodAuthLoginStart = "auth.login.start"
	MethodAuthLoginPoll  = "auth.login.poll"
	MethodAuthRefresh    = "auth.refresh"

	MethodFrontendAuthIdentifier   = "frontend_auth.identifier"
	MethodFrontendAuthAuthenticate = "frontend_auth.authenticate"

	MethodSchedulerPick = "scheduler.pick"
	MethodModelRoute    = "model.route"

	MethodExecutorIdentifier    = "executor.identifier"
	MethodExecutorExecute       = "executor.execute"
	MethodExecutorExecuteStream = "executor.execute_stream"
	MethodExecutorCountTokens   = "executor.count_tokens"
	MethodExecutorHTTPRequest   = "executor.http_request"

	MethodRequestTranslate       = "request.translate"
	MethodRequestNormalize       = "request.normalize"
	MethodRequestInterceptBefore = "request.intercept_before"
	MethodRequestInterceptAfter  = "request.intercept_after"

	MethodResponseTranslate            = "response.translate"
	MethodResponseNormalizeBefore      = "response.normalize_before"
	MethodResponseNormalizeAfter       = "response.normalize_after"
	MethodResponseInterceptAfter       = "response.intercept_after"
	MethodResponseInterceptStreamChunk = "response.intercept_stream_chunk"

	MethodThinkingIdentifier = "thinking.identifier"
	MethodThinkingApply      = "thinking.apply"

	MethodUsageHandle = "usage.handle"

	MethodCommandLineRegister = "command_line.register"
	MethodCommandLineExecute  = "command_line.execute"

	MethodManagementRegister = "management.register"
	MethodManagementHandle   = "management.handle"

	MethodHostHTTPDo             = "host.http.do"
	MethodHostHTTPDoStream       = "host.http.do_stream"
	MethodHostHTTPStreamRead     = "host.http.stream_read"
	MethodHostHTTPStreamClose    = "host.http.stream_close"
	MethodHostModelExecute       = "host.model.execute"
	MethodHostModelExecuteStream = "host.model.execute_stream"
	MethodHostModelStreamRead    = "host.model.stream_read"
	MethodHostModelStreamClose   = "host.model.stream_close"
	MethodHostStreamEmit         = "host.stream.emit"
	MethodHostStreamClose        = "host.stream.close"
	MethodHostLog                = "host.log"
	MethodHostAuthList           = "host.auth.list"
	MethodHostAuthGet            = "host.auth.get"
	MethodHostAuthGetRuntime     = "host.auth.get_runtime"
	MethodHostAuthSave           = "host.auth.save"
)
```

## Lifecycle

| Constant | Value | Request type | Response type |
|---|---|---|---|
| `MethodPluginRegister` | `plugin.register` | — | `registration` wire struct: `{schema_version, metadata Metadata, capabilities rpcCapabilities}` |
| `MethodPluginReconfigure` | `plugin.reconfigure` | raw config bytes for `plugins.configs.<pluginID>` | same as above |
| `MethodPluginShutdown` | `plugin.shutdown` | — | — (graceful drain signal before C `shutdown()`) |

## Model discovery

| Constant | Value | Request type | Response type |
|---|---|---|---|
| `MethodModelRegister` | `model.register` | `ModelRegistrationRequest{Plugin Metadata}` | `ModelRegistrationResponse{Provider string; Models []ModelInfo}` |
| `MethodModelStatic` | `model.static` | `StaticModelRequest{Plugin Metadata; Host HostConfigSummary}` | `ModelResponse{Provider string; Models []ModelInfo; AuthUpdate AuthData}` |
| `MethodModelForAuth` | `model.for_auth` | `AuthModelRequest{Plugin, AuthID, AuthProvider, StorageJSON, Metadata, Attributes, Host, HTTPClient}` | `ModelResponse` |

Implements `ModelRegistrar{RegisterModels}` and `ModelProvider{StaticModels, ModelsForAuth}`.

## Auth provider

| Constant | Value | Request type | Response type |
|---|---|---|---|
| `MethodAuthIdentifier` | `auth.identifier` | — | `{identifier string}` |
| `MethodAuthParse` | `auth.parse` | `AuthParseRequest{Provider, Path, FileName, RawJSON, Host}` | `AuthParseResponse{Handled bool; Auth AuthData; Auths []AuthData}` |
| `MethodAuthLoginStart` | `auth.login.start` | `AuthLoginStartRequest{Provider, BaseURL, Host, HTTPClient, Metadata}` | `AuthLoginStartResponse{Provider, URL, State string; ExpiresAt time.Time; Metadata}` |
| `MethodAuthLoginPoll` | `auth.login.poll` | `AuthLoginPollRequest{Provider, State, Host, HTTPClient, Metadata}` | `AuthLoginPollResponse{Status AuthLoginStatus; Message string; Auth AuthData; Auths []AuthData}` |
| `MethodAuthRefresh` | `auth.refresh` | `AuthRefreshRequest{AuthID, AuthProvider, StorageJSON, Metadata, Attributes, Host, HTTPClient}` | `AuthRefreshResponse{Auth AuthData; NextRefreshAfter time.Time}` |

Implements `AuthProvider{Identifier, ParseAuth, StartLogin, PollLogin, RefreshAuth}`.
`AuthLoginStatus` is one of `AuthLoginStatusPending`/`Success`/`Error` (`"pending"`/`"success"`/`"error"`).
Note the recurring `HTTPClient HostHTTPClient \`json:"-"\`` field — excluded from wire JSON; the
real cross-boundary RPC instead carries a `host_callback_id` (added by
`internal/pluginhost/rpc_schema.go` wrapper types) so the plugin can issue upstream HTTP via
`host.http.do`/`do_stream` using that opaque ID.

## Frontend auth (gatekeeping inbound API requests)

| Constant | Value | Request type | Response type |
|---|---|---|---|
| `MethodFrontendAuthIdentifier` | `frontend_auth.identifier` | — | `{identifier string}` |
| `MethodFrontendAuthAuthenticate` | `frontend_auth.authenticate` | `FrontendAuthRequest{Method, Path string; Headers http.Header; Query url.Values; Body []byte}` | `FrontendAuthResponse{Authenticated bool; Principal string; Metadata map[string]string}` |

Implements `FrontendAuthProvider{Identifier, Authenticate}`. Setting the wire flag
`FrontendAuthProviderExclusive: true` makes this the ONLY active request auth provider when
selected.

## Scheduling / routing

| Constant | Value | Request type | Response type | Notes |
|---|---|---|---|---|
| `MethodSchedulerPick` | `scheduler.pick` | `SchedulerPickRequest{Plugin, Provider, Providers, Model, Stream, Options, Candidates}` | `SchedulerPickResponse{AuthID, DelegateBuiltin string; Handled bool}` | Picks an auth candidate before the built-in scheduler runs. `DelegateBuiltin`: `"round-robin"` or `"fill-first"` (`SchedulerBuiltinRoundRobin`/`SchedulerBuiltinFillFirst`). |
| `MethodModelRoute` | `model.route` | `ModelRouteRequest{Plugin, PluginID, SourceFormat, RequestedModel, Stream, Headers, Query, Body, Metadata, AvailableProviders}` | `ModelRouteResponse{Handled bool; TargetKind ModelRouteTargetKind; Target, TargetModel, Reason string}` | Decides which executor/provider handles a request, before model→provider/auth resolution. `TargetKind`: `"self"`/`"executor"`/`"provider"` (`ModelRouteTargetSelf`/`Executor`/`Provider`). |

Implements `Scheduler{Pick}` and `ModelRouter{RouteModel}`.

## Executor (upstream model execution)

| Constant | Value | Request type | Response type |
|---|---|---|---|
| `MethodExecutorIdentifier` | `executor.identifier` | — | `{identifier string}` |
| `MethodExecutorExecute` | `executor.execute` | `ExecutorRequest{AuthID, AuthProvider, Model, Format, Stream, Alt, Headers, Query, OriginalRequest, SourceFormat, Payload, Metadata, StorageJSON, AuthMetadata, AuthAttributes, HTTPClient}` | `ExecutorResponse{Payload []byte; Headers http.Header; Metadata map[string]any}` |
| `MethodExecutorExecuteStream` | `executor.execute_stream` | `ExecutorRequest` | `ExecutorStreamResponse{Headers http.Header; Chunks <-chan ExecutorStreamChunk}` (`ExecutorStreamChunk{Payload []byte; Err error}`) |
| `MethodExecutorCountTokens` | `executor.count_tokens` | `ExecutorRequest` | `ExecutorResponse` |
| `MethodExecutorHTTPRequest` | `executor.http_request` | `ExecutorHTTPRequest{AuthID, AuthProvider, Method, URL, Headers, Body, StorageJSON, Metadata, Attributes, HTTPClient}` | `ExecutorHTTPResponse{StatusCode int; Headers http.Header; Body []byte}` |

Implements `ProviderExecutor{Identifier, Execute, ExecuteStream, CountTokens, HttpRequest}`.
Executors declaring the `Executor` capability MUST also declare non-empty
`ExecutorInputFormats`/`ExecutorOutputFormats` (e.g. `["chat-completions"]`) in the capability
block — the host requires at least one protocol string in each.
`ExecutorModelScope`: `"both"` (default)/`"static"`/`"oauth"`.

## Request/response transformation

| Constant | Value | Request type | Response type |
|---|---|---|---|
| `MethodRequestTranslate` | `request.translate` | `RequestTransformRequest{FromFormat, ToFormat, Model string; Stream bool; Body []byte}` | `PayloadResponse{Body []byte}` |
| `MethodRequestNormalize` | `request.normalize` | `RequestTransformRequest` | `PayloadResponse` |
| `MethodRequestInterceptBefore` | `request.intercept_before` | `RequestInterceptRequest{SourceFormat, ToFormat, Model, RequestedModel string; Stream bool; Headers, Body, Metadata}` | `RequestInterceptResponse{Headers http.Header; Body []byte; ClearHeaders []string}` |
| `MethodRequestInterceptAfter` | `request.intercept_after` | `RequestInterceptRequest` | `RequestInterceptResponse` |
| `MethodResponseTranslate` | `response.translate` | `ResponseTransformRequest{FromFormat, ToFormat, Model string; Stream bool; OriginalRequest, TranslatedRequest, Body []byte}` | `PayloadResponse` |
| `MethodResponseNormalizeBefore` | `response.normalize_before` | `ResponseTransformRequest` | `PayloadResponse` |
| `MethodResponseNormalizeAfter` | `response.normalize_after` | `ResponseTransformRequest` | `PayloadResponse` |
| `MethodResponseInterceptAfter` | `response.intercept_after` | `ResponseInterceptRequest{SourceFormat, Model, RequestedModel string; Stream bool; RequestHeaders, ResponseHeaders, OriginalRequest, RequestBody, Body, StatusCode, Metadata}` | `ResponseInterceptResponse{Headers http.Header; Body []byte; ClearHeaders []string}` |
| `MethodResponseInterceptStreamChunk` | `response.intercept_stream_chunk` | `StreamChunkInterceptRequest{SourceFormat, Model, RequestedModel, RequestHeaders, ResponseHeaders, OriginalRequest, RequestBody, Body, HistoryChunks, ChunkIndex, Metadata}` | `StreamChunkInterceptResponse{Headers, Body, ClearHeaders, DropChunk bool}` |

Implements `RequestTranslator{TranslateRequest}`, `RequestNormalizer{NormalizeRequest}`,
`ResponseTranslator{TranslateResponse}`, `ResponseNormalizer{NormalizeResponse}` (used by both
`ResponseBeforeTranslator` and `ResponseAfterTranslator` capability slots),
`RequestInterceptor{InterceptRequestBeforeAuth, InterceptRequestAfterAuth}`,
`ResponseInterceptor{InterceptResponse}`, `StreamChunkInterceptor{InterceptStreamChunk}`.

Interceptor notes: `RequestInterceptRequest.ToFormat` is empty before credential selection;
`Model` is the current execution model (post-selection = upstream model); `RequestedModel` is the
client-requested model before alias/pool rewriting. `*InterceptResponse.ClearHeaders` is applied
BEFORE `Headers`. `StreamChunkInterceptRequest.HistoryChunks` retains at most 64 chunks / 1 MiB
total; `ChunkIndex` is 0-based, and `StreamChunkHeaderInitIndex` (`-1`) marks a header-only init
call. `StreamChunkInterceptResponse.DropChunk` skips delivery and excludes the chunk from
`HistoryChunks`, but header updates still apply.

## Thinking / reasoning

| Constant | Value | Request type | Response type |
|---|---|---|---|
| `MethodThinkingIdentifier` | `thinking.identifier` | — | `{identifier string}` |
| `MethodThinkingApply` | `thinking.apply` | `ThinkingApplyRequest{Provider string; Model ModelInfo; Config ThinkingConfig; Body []byte}` | `PayloadResponse{Body []byte}` |

Implements `ThinkingApplier{Identifier, ApplyThinking}`.
`ThinkingConfig{Mode string; Budget int; Level string}` — `Mode` is one of `budget`/`level`/`none`/`auto`.

## Usage / billing

| Constant | Value | Request type | Response type |
|---|---|---|---|
| `MethodUsageHandle` | `usage.handle` | `UsageRecord{Provider, ExecutorType, Model, Alias, APIKey, AuthID, AuthIndex, AuthType, Source, ReasoningEffort, ServiceTier string; Generate bool; RequestedAt time.Time; Latency, TTFT time.Duration; Failed bool; Failure UsageFailure; Detail UsageDetail; ResponseHeaders http.Header}` | — (fire-and-forget) |

Implements `UsagePlugin{HandleUsage}` — no return value, observability sink. `Generate` is
normalized to `true` by the host before delivery if omitted.
`UsageDetail{InputTokens, OutputTokens, ReasoningTokens, CachedTokens, CacheReadTokens, CacheCreationTokens, TotalTokens int64}`.

## CLI flags

| Constant | Value | Request type | Response type |
|---|---|---|---|
| `MethodCommandLineRegister` | `command_line.register` | `CommandLineRegistrationRequest{Plugin Metadata}` | `CommandLineRegistrationResponse{Flags []CommandLineFlag}` |
| `MethodCommandLineExecute` | `command_line.execute` | `CommandLineExecutionRequest{Plugin, Program, Args, ConfigPath, Host, Flags, TriggeredFlags}` | `CommandLineExecutionResponse{Stdout, Stderr []byte; Auths []AuthData; ExitCode int}` |

Implements `CommandLinePlugin{RegisterCommandLine, ExecuteCommandLine}`.
`CommandLineFlag{Name, Usage, Type, DefaultValue string}` — `Type` is one of
`bool`/`string`/`int`/`int64`/`float64`/`duration`. `CommandLineExecutionResponse.Auths` are
persisted by the host.

## Management API / resources

| Constant | Value | Request type | Response type |
|---|---|---|---|
| `MethodManagementRegister` | `management.register` | `ManagementRegistrationRequest{Plugin Metadata; BasePath, ResourceBasePath string}` | `ManagementRegistrationResponse{Routes []ManagementRoute; Resources []ResourceRoute}` |
| `MethodManagementHandle` | `management.handle` | `ManagementRequest{Method, Path string; Headers, Query, Body}` | `ManagementResponse{StatusCode int; Headers http.Header; Body []byte}` (`StatusCode 0` ⇒ 200) |

Implements `ManagementAPI{RegisterManagement}` and per-route `ManagementHandler{HandleManagement}`.
`ManagementRoute{Method, Path, Menu, Description string; Handler ManagementHandler}` — GET routes
with `Menu` set are legacy-also-exposed under `/v0/resource/plugins/<id>/`. `ResourceRoute{Path,
Menu, Description string; Handler ManagementHandler}` — resource requests are **not**
management-authenticated. `BasePath` is the only Management API prefix a plugin may register
under.

## Host callback methods (plugin → host)

See `${CLAUDE_PLUGIN_ROOT}/skills/cpa-plugin-abi/references/host-callbacks.md` for the full
per-method breakdown; summary:

| Constant | Value |
|---|---|
| `MethodHostHTTPDo` | `host.http.do` |
| `MethodHostHTTPDoStream` | `host.http.do_stream` |
| `MethodHostHTTPStreamRead` | `host.http.stream_read` |
| `MethodHostHTTPStreamClose` | `host.http.stream_close` |
| `MethodHostModelExecute` | `host.model.execute` |
| `MethodHostModelExecuteStream` | `host.model.execute_stream` |
| `MethodHostModelStreamRead` | `host.model.stream_read` |
| `MethodHostModelStreamClose` | `host.model.stream_close` |
| `MethodHostStreamEmit` | `host.stream.emit` |
| `MethodHostStreamClose` | `host.stream.close` |
| `MethodHostLog` | `host.log` |
| `MethodHostAuthList` | `host.auth.list` |
| `MethodHostAuthGet` | `host.auth.get` |
| `MethodHostAuthGetRuntime` | `host.auth.get_runtime` |
| `MethodHostAuthSave` | `host.auth.save` |

## Capability flags on the wire

`plugin.register`/`plugin.reconfigure` report capabilities as a flat struct of booleans + a few
scope/format fields (`internal/pluginhost/rpc_schema.go: rpcCapabilities`):

```go
type rpcCapabilities struct {
    ModelRegistrar                bool
    ModelProvider                 bool
    AuthProvider                  bool
    FrontendAuthProvider          bool
    FrontendAuthProviderExclusive bool
    Scheduler                     bool
    ModelRouter                   bool
    Executor                      bool
    ExecutorModelScope            pluginapi.ExecutorModelScope   // "both" | "static" | "oauth"
    ExecutorInputFormats          []string
    ExecutorOutputFormats         []string
    RequestTranslator             bool
    RequestNormalizer             bool
    RequestInterceptor            bool
    ResponseTranslator            bool
    ResponseBeforeTranslator      bool
    ResponseAfterTranslator       bool
    ResponseInterceptor           bool
    StreamChunkInterceptor        bool  // json tag: "response_stream_interceptor"
    ThinkingApplier                bool
    UsagePlugin                    bool
    CommandLinePlugin              bool
    ManagementAPI                  bool
}
```

Each capability on the host-side Go `Capabilities` struct is a Go **interface**; a plugin
"declares" a capability by implementing that interface and reporting `true` for the corresponding
boolean on the wire — the host never introspects Go types across the ABI boundary, it only trusts
the boolean flags and routes method calls accordingly. The example plugin's exact wire test
values (`exampleRegistration()`):

```go
Capabilities: registrationCapability{
    ModelRegistrar: true, ModelProvider: true, AuthProvider: true, FrontendAuthProvider: true,
    Executor: true, ExecutorModelScope: pluginapi.ExecutorModelScopeBoth,
    ExecutorInputFormats: []string{"chat-completions"}, ExecutorOutputFormats: []string{"chat-completions"},
    RequestTranslator: true, RequestNormalizer: true,
    ResponseTranslator: true, ResponseBeforeTranslator: true, ResponseAfterTranslator: true,
    ThinkingApplier: true, UsagePlugin: true, CommandLinePlugin: true, ManagementAPI: true,
},
```

The minimal executor-only capability block (`examples/plugin/executor/c/src/plugin.c`):

```json
{"executor": true, "executor_model_scope": "both", "executor_input_formats": ["chat-completions"], "executor_output_formats": ["chat-completions"]}
```
