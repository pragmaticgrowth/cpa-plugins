# Real-world Go plugin patterns

Concrete, copy-pasteable patterns pulled from the vendored upstream examples (pinned v7.2.88):
`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/simple/go/`,
`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/codex-service-tier/go/`,
`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/claude-web-search-router/go/`.

For the base cgo skeleton and required `Metadata`/`Capabilities` fields, see `skeleton.md` first.

## Config parsing: `plugin.register`/`reconfigure` → YAML

The RPC request body for both `plugin.register` and `plugin.reconfigure` is:

```go
type lifecycleRequest struct {
	ConfigYAML []byte `json:"config_yaml"`
}
```

The host does **not** pre-parse your config — it hands you raw, normalized YAML bytes from
`plugins.configs.<pluginID>` inside that JSON envelope. You own the schema end-to-end via
`gopkg.in/yaml.v3` and `yaml:"..."` struct tags. `ConfigFields` in `Metadata` is descriptive-only
(drives the management UI), never enforced.

**Single scalar config** — `codex-service-tier` (real config: one boolean field), stored in an
`atomic.Bool` because it's read on every request on the hot path:

```go
var fastEnabled atomic.Bool

type pluginConfig struct {
	Fast bool `yaml:"fast"`
}

func configure(raw []byte) error {
	var req lifecycleRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return err
		}
	}
	cfg := pluginConfig{}
	if len(req.ConfigYAML) > 0 {
		fast, err := decodeFastConfig(req.ConfigYAML)
		if err != nil {
			return err
		}
		cfg.Fast = fast
	}
	fastEnabled.Store(cfg.Fast)
	return nil
}

func decodeFastConfig(configYAML []byte) (bool, error) {
	var cfg pluginConfig
	if err := yaml.Unmarshal(configYAML, &cfg); err != nil {
		return false, err
	}
	return cfg.Fast, nil
}
```

**Multi-field config with defaults** — `claude-web-search-router` (real config: 9 fields
including an enum and a string slice), stored in an `atomic.Value` holding an *immutable* struct,
replaced wholesale on every `configure()` call, read via a `loadedConfig()` getter that falls
back to defaults if nothing has been stored yet:

```go
var currentConfig atomic.Value

type pluginConfig struct {
	Enabled              bool     `yaml:"enabled"`
	Route                string   `yaml:"route"`
	AntigravityModel     string   `yaml:"antigravity_model"`
	CodexModel           string   `yaml:"codex_model"`
	XAIModel             string   `yaml:"xai_model"`
	DefaultProvider      string   `yaml:"default_provider"`
	DefaultProviderModel string   `yaml:"default_provider_model"`
	TavilyAPIKeys        []string `yaml:"tavily_api_keys"`
	RequireWebSearchOnly bool     `yaml:"require_web_search_only"`
}

func defaultPluginConfig() pluginConfig {
	return pluginConfig{
		Enabled:              true,
		Route:                string(backendFallback),
		RequireWebSearchOnly: true,
	}
}

func decodeConfig(raw []byte) (pluginConfig, error) {
	cfg := defaultPluginConfig() // seed defaults BEFORE unmarshal, so omitted YAML
	if err := yaml.Unmarshal(raw, &cfg); err != nil { // fields don't silently zero out
		return pluginConfig{}, err
	}
	cfg.Route = strings.TrimSpace(cfg.Route)
	// ...trim/normalize the rest...
	return cfg, nil
}

func configure(raw []byte) error {
	var req lifecycleRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return err
		}
	}
	cfg := defaultPluginConfig()
	if len(req.ConfigYAML) > 0 {
		decoded, err := decodeConfig(req.ConfigYAML)
		if err != nil {
			return err
		}
		cfg = decoded
	}
	currentConfig.Store(cfg)
	return nil
}

func loadedConfig() pluginConfig {
	if cfg, ok := currentConfig.Load().(pluginConfig); ok {
		return cfg
	}
	return defaultPluginConfig()
}
```

**Rule of thumb**: 1 scalar field → `atomic.Bool`/`atomic.Int64` with direct load. 2+ fields →
`atomic.Value` holding an immutable struct, replaced wholesale, never mutated in place under
concurrent reads. Always seed a defaults struct before `yaml.Unmarshal` — plain
`yaml.Unmarshal(raw, &pluginConfig{})` leaves any field the user's YAML omits at Go's zero value.

`ConfigFieldTypeArray` for a string-slice field (`tavily_api_keys`):

```go
{Name: "tavily_api_keys", Type: pluginapi.ConfigFieldTypeArray, Description: "Tavily API keys (round-robin) when route=tavily."}
```

`ConfigFieldTypeEnum` referencing host-provided built-in constants (scheduler delegation):

```go
{
	Name:        "delegate",
	Type:        pluginapi.ConfigFieldTypeEnum,
	EnumValues:  []string{"", pluginapi.SchedulerBuiltinFillFirst, pluginapi.SchedulerBuiltinRoundRobin},
	Description: "Delegates selection to a built-in scheduler when set to fill-first or round-robin.",
}
```

## Mutating a request body: `sjson` (surgical write) vs `gjson` (read-only query)

`codex-service-tier`'s `request.normalize` handler — set one field without a full
unmarshal/remarshal round trip, and **fail open** (return the unmodified body) whenever the
plugin isn't confident it should act:

```go
func normalizeRequest(raw []byte) ([]byte, error) {
	var req pluginapi.RequestTransformRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	body := req.Body
	if !shouldSetPriorityServiceTier(req) {
		return okEnvelope(pluginapi.PayloadResponse{Body: body})
	}
	updated, ok := setPriorityServiceTier(body)
	if !ok {
		return okEnvelope(pluginapi.PayloadResponse{Body: body})
	}
	return okEnvelope(pluginapi.PayloadResponse{Body: updated})
}

func shouldSetPriorityServiceTier(req pluginapi.RequestTransformRequest) bool {
	if !fastEnabled.Load() {
		return false
	}
	if !strings.EqualFold(req.ToFormat, "codex") {
		return false
	}
	return req.Model == "gpt-5.5"
}

func setPriorityServiceTier(body []byte) ([]byte, bool) {
	updated, err := sjson.SetBytes(body, "service_tier", "priority")
	if err != nil {
		return nil, false
	}
	return updated, true
}
```

`pluginapi.RequestTransformRequest` fields: `FromFormat`, `ToFormat`, `Model`, `Stream`, `Body
[]byte`. `pluginapi.PayloadResponse{Body []byte}` is the universal transform response shape,
reused by `request.translate`/`request.normalize` and every `response.*` transform.

`claude-web-search-router`'s `detect.go` shows the read-only counterpart, `gjson`, for cheap
structural checks without a full unmarshal (checking for a tool type inside a JSON array):

```go
func hasClaudeTypedWebSearchTool(body []byte) bool {
	tools := gjson.GetBytes(body, "tools")
	if !tools.IsArray() {
		return false
	}
	for _, tool := range tools.Array() {
		if isClaudeTypedWebSearchToolType(tool.Get("type").String()) {
			return true
		}
	}
	return false
}
```

Use `sjson` when you're writing one or two fields into an otherwise-opaque JSON body; use `gjson`
when you only need to inspect/branch on a body's shape without producing a new one; use full
`encoding/json` unmarshal-into-`map[string]any`-mutate-remarshal (see `simple/go`'s
`applyThinking`) when you need to inject/modify several arbitrary fields with no fixed schema.

## Calling back into the host: `callHost`

Any plugin that calls the host (not just responds to it) needs the `store_host_api` C helper
block in the cgo preamble (see `skeleton.md` §1) plus this Go-side wrapper, used verbatim across
`claude-web-search-router` and every host-callback example:

```go
func callHost(method string, payload any) (json.RawMessage, error) {
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal host callback %s: %w", method, err)
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))

	var response C.cliproxy_buffer
	var requestPtr *C.uint8_t
	if len(rawPayload) > 0 {
		cPayload := C.CBytes(rawPayload)
		if cPayload == nil {
			return nil, fmt.Errorf("allocate host callback %s", method)
		}
		defer C.free(cPayload)
		requestPtr = (*C.uint8_t)(cPayload)
	}
	callCode := C.call_host_api(cMethod, requestPtr, C.size_t(len(rawPayload)), &response)
	var rawResponse []byte
	if response.ptr != nil && response.len > 0 {
		rawResponse = C.GoBytes(response.ptr, C.int(response.len))
	}
	if response.ptr != nil {
		C.free_host_buffer(response.ptr, response.len) // host-owned buffer: free via the HOST's free_buffer
	}
	if len(rawResponse) == 0 {
		return nil, fmt.Errorf("host callback %s returned no response, code=%d", method, int(callCode))
	}

	var env envelope
	if err := json.Unmarshal(rawResponse, &env); err != nil {
		return nil, fmt.Errorf("decode host envelope %s: %w", method, err)
	}
	if !env.OK {
		if env.Error != nil {
			return nil, fmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)
		}
		return nil, fmt.Errorf("host callback %s failed", method)
	}
	if callCode != 0 {
		return nil, fmt.Errorf("host callback %s returned code=%d", method, int(callCode))
	}
	return append(json.RawMessage(nil), env.Result...), nil
}
```

Key points:
- The **plugin** allocates the request buffer (`C.CBytes`) and frees it itself (`defer
  C.free(...)`).
- The **response** buffer is host-owned — release it with `C.free_host_buffer` (routes to
  `stored_host->free_buffer`), never plain `C.free`. Mixing these up is a double-free/UAF bug.
- Check **both** a non-zero `callCode` and `env.OK == false` as failure signals.

### `host.http.*` — outbound HTTP under host transport policy

`host.http.do` performs an HTTP request under host control (transport policy, auth context,
request logging) — call it instead of a raw `net/http` client whenever you need the *host* to
mediate an outbound call. Arbitrary third-party APIs unrelated to the host's own model backends
(e.g. a search provider) are free to use plain `net/http` directly — only calls that need the
host's auth/quota/logging benefits should go through `host.http.*`/`host.model.*`.

### `host.auth.*` — reading/writing auth credential files

```go
func callHostAuthList() (authListResponse, error) {
	result, err := callHost(pluginabi.MethodHostAuthList, map[string]any{})
	// ...
}

func callHostAuthGet(authIndex string) (pluginapi.HostAuthGetResponse, error) {
	result, err := callHost(pluginabi.MethodHostAuthGet, pluginapi.HostAuthGetRequest{AuthIndex: authIndex})
	// ...
}

func callHostAuthSave(name string, rawJSON json.RawMessage) (pluginapi.HostAuthSaveResponse, error) {
	result, err := callHost(pluginabi.MethodHostAuthSave, pluginapi.HostAuthSaveRequest{
		Name: name,
		JSON: rawJSON,
	})
	// ...
}
```

Relevant methods: `pluginabi.MethodHostAuthList`, `MethodHostAuthGet`, `MethodHostAuthGetRuntime`,
`MethodHostAuthSave`. Relevant types: `pluginapi.HostAuthFileEntry`,
`HostAuthGetRequest{AuthIndex string}`, `HostAuthGetResponse`, `HostAuthGetRuntimeResponse`,
`HostAuthSaveRequest{Name string; JSON json.RawMessage}`, `HostAuthSaveResponse`.

### `host.model.*` — a nested/secondary model call (non-streaming)

For any plugin that needs to make a secondary LLM call to satisfy a request (e.g. a router's own
executor calling a different backend model):

```go
func executeOnce(opts runOptions) (pluginapi.HostModelExecutionResponse, error) {
	body, _ := modelRequestBody(opts)
	result, err := callHost(pluginabi.MethodHostModelExecute, hostModelExecutionRequest{
		HostModelExecutionRequest: pluginapi.HostModelExecutionRequest{
			EntryProtocol: opts.EntryProtocol, // e.g. "openai" / "claude"
			ExitProtocol:  opts.ExitProtocol,
			Model:         opts.Model,
			Stream:        false,
			Body:          body,
			Headers:       cloneHeader(opts.Headers),
			Query:         cloneValues(opts.Query),
			Alt:           opts.Alt,
		},
		HostCallbackID: opts.HostCallbackID,
	})
	var resp pluginapi.HostModelExecutionResponse
	_ = json.Unmarshal(result, &resp)
	return resp, err
}
```

`pluginapi.HostModelExecutionRequest{EntryProtocol, ExitProtocol, Model, Stream, Body, Headers,
Query, Alt}` → `pluginapi.HostModelExecutionResponse{StatusCode, Headers, Body}`.

**Critical: forward `HostCallbackID`.** It tells the host to skip *this plugin's own*
interceptor/normalizer chain on the nested call — host model callbacks are never recursively
routed back through the calling plugin's own request pipeline. Omitting it risks reentrancy or
double-transformation.

## Streaming

Two independent stream-ID flows exist. Do not conflate them.

### (a) Plugin *produces* an outbound stream, as an executor

`executor.execute_stream` returns **immediately** with headers only; the real work happens in a
background goroutine that pushes chunks via `host.stream.emit` and finishes with
`host.stream.close`. The `stream_id` is supplied by the **host** up front, in the RPC request:

```go
func executeStream(raw []byte) ([]byte, error) {
	var req rpcExecutorRequest
	_ = json.Unmarshal(raw, &req)
	return startExecutorStream(req, runWebSearchStreamOrchestration, closePluginStream)
}

func startExecutorStream(req rpcExecutorRequest, runner streamOrchestrationRunner, closeStream pluginStreamCloser) ([]byte, error) {
	streamID := strings.TrimSpace(req.StreamID)
	if streamID == "" {
		return errorEnvelope("executor_error", "stream_id is required for executor.execute_stream"), nil
	}
	go func() {
		defer func() {
			if r := recover(); r != nil { // ALWAYS recover in a streaming goroutine —
				closeStream(streamID, fmt.Sprintf("stream orchestration panic: %v", r)) // an unrecovered panic
			} // crashes the whole host process (in-process plugin)
		}()
		if err := runner(context.Background(), req.ExecutorRequest, req.HostCallbackID, streamID); err != nil {
			closeStream(streamID, err.Error())
			return
		}
		closeStream(streamID, "")
	}()
	return okEnvelope(map[string]any{
		"headers": http.Header{"Content-Type": []string{"text/event-stream"}},
	})
}

func emitPluginStreamChunk(streamID string, payload []byte) error {
	_, err := callHost(pluginabi.MethodHostStreamEmit, rpcStreamEmitRequest{StreamID: streamID, Payload: payload})
	return err
}

func closePluginStream(streamID, errMsg string) {
	_, _ = callHost(pluginabi.MethodHostStreamClose, rpcStreamCloseRequest{StreamID: streamID, Error: strings.TrimSpace(errMsg)})
}
```

### (b) Plugin *consumes* a nested host model stream

The plugin calls `host.model.execute_stream`, the host hands back a *different* `StreamID`; the
plugin reads with `host.model.stream_read` in a loop until `Done`, then closes with
`host.model.stream_close` — unless the request set `implicit_close: true`, in which case the host
auto-closes the stream when the enclosing RPC scope (e.g. `management.handle`) returns.

```go
func readHostModelStream(streamID string) (pluginapi.HostModelStreamReadResponse, error) {
	result, err := callHost(pluginabi.MethodHostModelStreamRead, pluginapi.HostModelStreamReadRequest{StreamID: streamID})
	var resp pluginapi.HostModelStreamReadResponse
	_ = json.Unmarshal(result, &resp)
	return resp, err
}

func closeHostModelStream(streamID string) error {
	_, err := callHost(pluginabi.MethodHostModelStreamClose, pluginapi.HostModelStreamCloseRequest{StreamID: streamID})
	return err
}

// Forwarding a nested host model stream into the plugin's OWN outbound stream — bridges (a) and (b):
func hostModelStreamForwardClaude(ctx context.Context, hostCallbackID, execModel string, body []byte, pluginStreamID string) (int, error) {
	raw, _ := callHost(pluginabi.MethodHostModelExecuteStream, hostModelExecutionRequest{ /* ... */ })
	var resp pluginapi.HostModelStreamResponse
	_ = json.Unmarshal(raw, &resp)
	if resp.StatusCode >= 400 {
		_ = closeHostModelStream(resp.StreamID)
		return resp.StatusCode, fmt.Errorf("host model status %d", resp.StatusCode)
	}
	defer func() { _ = closeHostModelStream(resp.StreamID) }()

	for {
		chunkRaw, _ := callHost(pluginabi.MethodHostModelStreamRead, pluginapi.HostModelStreamReadRequest{StreamID: resp.StreamID})
		var chunk pluginapi.HostModelStreamReadResponse
		_ = json.Unmarshal(chunkRaw, &chunk)
		if chunk.Error != "" {
			return hostHTTPStatusFromError(fmt.Errorf("%s", chunk.Error)), fmt.Errorf("%s", chunk.Error)
		}
		if len(chunk.Payload) > 0 {
			if err := emitPluginStreamChunk(pluginStreamID, bytes.Clone(chunk.Payload)); err != nil { // defensive copy
				return 0, err
			}
		}
		if chunk.Done {
			break
		}
	}
	return http.StatusOK, nil
}
```

`pluginapi.HostModelStreamResponse{StatusCode, Headers, StreamID}`,
`HostModelStreamReadRequest{StreamID}`, `HostModelStreamReadResponse{Payload, Error, Done}`,
`HostModelStreamCloseRequest{StreamID}`. Always check `chunk.Error` **before** `chunk.Done` — an
error chunk may or may not also set `Done`. `bytes.Clone` the payload before re-emitting so the
outbound emit doesn't alias a buffer the host may reuse/free.

## Model routing: self / executor / provider

`model.route` decides where a matching request goes. Request/response types:

```go
type ModelRouteRequest struct {
	Plugin             Metadata // metadata of the plugin being executed
	PluginID           string
	SourceFormat       string   // original client protocol format
	RequestedModel     string
	Stream             bool
	Headers            http.Header
	Query              url.Values
	Body               []byte
	Metadata           map[string]any
	AvailableProviders []string // built-in provider keys with auth currently registered (read-only)
}

type ModelRouteTargetKind string
const (
	ModelRouteTargetSelf     ModelRouteTargetKind = "self"     // route to the router plugin's own executor
	ModelRouteTargetExecutor ModelRouteTargetKind = "executor" // route to a specific plugin executor
	ModelRouteTargetProvider ModelRouteTargetKind = "provider" // route through the built-in auth/executor path
)

type ModelRouteResponse struct {
	Handled     bool
	TargetKind  ModelRouteTargetKind
	Target      string // plugin executor id (TargetKind=executor) or provider key (TargetKind=provider)
	TargetModel string // TargetKind=provider only; empty keeps the client-requested model
	Reason      string // free-text diagnostic
}
```

`claude-web-search-router`'s `routeModel` — the real decision tree, demonstrating both
`TargetProvider` (single-backend pin, no further plugin involvement) and `TargetSelf`
(orchestrated multi-backend fallback, handled by this plugin's own `executor.execute`):

```go
func routeModel(raw []byte) ([]byte, error) {
	var req rpcModelRouteRequest // embeds pluginapi.ModelRouteRequest + HostCallbackID
	_ = json.Unmarshal(raw, &req)
	cfg := loadedConfig()
	if !cfg.Enabled {
		return okEnvelope(pluginapi.ModelRouteResponse{Handled: false})
	}
	if !isClaudeSourceFormat(req.SourceFormat) {
		return okEnvelope(pluginapi.ModelRouteResponse{Handled: false})
	}
	if !isClaudeCodeBuiltinWebSearchRequest(req.Body, cfg.RequireWebSearchOnly) {
		return okEnvelope(pluginapi.ModelRouteResponse{Handled: false})
	}
	route := strings.TrimSpace(cfg.Route)
	if isFallbackRoute(route) {
		return okEnvelope(routeWithFallback(cfg, req.ModelRouteRequest))
	}
	if plans := executionPlansForRoute(cfg, req.ModelRouteRequest, route); len(plans) > 0 {
		return okEnvelope(pluginapi.ModelRouteResponse{
			Handled:    true,
			TargetKind: pluginapi.ModelRouteTargetSelf, // this plugin's own executor.execute[_stream] runs it
			Reason:     "claude_code_web_search_orchestrated",
		})
	}
	backend := routeBackend(route)
	resp, ok := tryRouteBackend(backend, cfg, req.ModelRouteRequest) // may yield TargetKind: provider
	if ok {
		return okEnvelope(resp)
	}
	if strings.TrimSpace(resp.Reason) != "" {
		return okEnvelope(resp)
	}
	return okEnvelope(pluginapi.ModelRouteResponse{Handled: false})
}
```

`hasProvider(req.AvailableProviders, key)` — always check a provider is actually available for
*this* request before committing to a `TargetProvider` route; never assume configuration.

**Gotcha**: `ModelRouteRequest.AvailableProviders` is populated on `model.route` but **not**
passed on `executor.execute`/`executor.execute_stream` — a plugin that needs the same
availability check in both places must fall back to a looser "lenient" runnability check inside
the executor path (`claude-web-search-router` calls this `backendRunnableLenient`).

**`Handled: false` = polite decline vs. `errorEnvelope(...)` = active rejection.** A router that
declines lets the host or another plugin try; a router that actively wants to reject a request
(e.g. `scheduler.pick`'s `Deny` config) should return an `errorEnvelope`, not `Handled: false`.

## Executor: non-streaming with penalty-aware, ordered backend fallback

`claude-web-search-router`'s `execute` handler and `runOrderedExecutionPlans` — retries the next
backend on retryable HTTP status, self-tunes via an in-memory penalty score (no config, no
persistence):

```go
func execute(raw []byte) ([]byte, error) {
	var req rpcExecutorRequest // embeds pluginapi.ExecutorRequest + StreamID + HostCallbackID
	_ = json.Unmarshal(raw, &req)
	body, headers, err := runWebSearchWithExecutionFallback(context.Background(), req.ExecutorRequest, req.HostCallbackID)
	if err != nil {
		return errorEnvelope("executor_error", err.Error()), nil
	}
	return okEnvelope(pluginapi.ExecutorResponse{Payload: body, Headers: headers})
}

func isRetryableHTTPStatus(code int) bool { return code == 429 || code == 503 || code == 502 }

const (
	penaltyBumpOn429503 = 5
	penaltyDecaySuccess = 1
)

var backendPenalties = struct {
	sync.Mutex
	scores map[routeBackend]int
}{scores: make(map[routeBackend]int)}

func recordBackendFailure(b routeBackend) {
	backendPenalties.Lock()
	defer backendPenalties.Unlock()
	backendPenalties.scores[b] += penaltyBumpOn429503
}

func recordBackendSuccess(b routeBackend) {
	backendPenalties.Lock()
	defer backendPenalties.Unlock()
	if s := backendPenalties.scores[b] - penaltyDecaySuccess; s < 0 {
		backendPenalties.scores[b] = 0
	} else {
		backendPenalties.scores[b] = s
	}
}

func sortBackendsByPenalty(backends []routeBackend) []routeBackend {
	out := append([]routeBackend(nil), backends...)
	sort.SliceStable(out, func(i, j int) bool { return penaltyScore(out[i]) < penaltyScore(out[j]) })
	return out
}
```

`pluginapi.ExecutorRequest` fields used: `AuthID`, `AuthProvider`, `Model`, `Format`, `Stream`,
`Alt`, `Headers`, `Query`, `OriginalRequest []byte`, `SourceFormat`. `pluginapi.ExecutorResponse{
Payload []byte, Headers http.Header, Metadata map[string]any }`.

`executor.count_tokens` and `executor.http_request` are separate methods with their own
`pluginapi.ExecutorResponse`/`ExecutorHTTPResponse{StatusCode, Body, Headers}` — a minimal
executor can stub `count_tokens` to `{"input_tokens":0}` if the host's usage/billing logic
doesn't depend on it being accurate; a production executor should return real counts if it does.

## Management API pages: HTML escaping + query/body merge

A `management-api` plugin's `management.handle` is effectively a tiny embedded HTTP handler —
parse `req.Query`/`req.Body`, build an `http.StatusCode` + `http.Header` + raw `[]byte` body:

```go
type managementRequest struct {
	Method         string
	Path           string
	Headers        http.Header
	Query          url.Values
	Body           []byte
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type managementResponse struct {
	StatusCode int         `json:"StatusCode"`
	Headers    http.Header `json:"Headers"`
	Body       []byte      `json:"Body"`
}
```

Always escape interpolated values — the host does not sanitize management-page HTML:

```go
func writeDefinition(out *bytes.Buffer, key, value string) {
	out.WriteString("<dt>")
	out.WriteString(html.EscapeString(key))
	out.WriteString("</dt><dd><code>")
	out.WriteString(html.EscapeString(value))
	out.WriteString("</code></dd>")
}
```

`management.register` (separate from `plugin.register`) declares menu resources, served under
`/v0/resource/plugins/<pluginID>/<path>`:

```go
case pluginabi.MethodManagementRegister:
	return okEnvelope(managementRegistrationResponse{Resources: []pluginapi.ResourceRoute{{
		Path:        "/status",
		Menu:        "Example Plugin",
		Description: "Shows example plugin status as a browser-navigable resource.",
	}}})
case pluginabi.MethodManagementHandle:
	return handleManagement(request)
```

## Full walkthrough: `codex-service-tier` (smallest real plugin)

Single capability (`request_normalizer`), single config field (`fast bool`). What it does: on
outbound Codex `gpt-5.5` Responses API requests, sets `service_tier: "priority"` — but only when
`fast: true` is configured. Full source:
`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/codex-service-tier/go/main.go`. Structure:

1. `atomic.Bool` config (`fastEnabled`) — see "Config parsing" above.
2. `handleMethod` has exactly two cases: `plugin.register`/`plugin.reconfigure` (calls
   `configure()` then returns `pluginRegistration()`), and `request.normalize` (calls
   `normalizeRequest()`).
3. `normalizeRequest` unmarshals `pluginapi.RequestTransformRequest`, checks
   `fastEnabled.Load() && req.ToFormat == "codex" && req.Model == "gpt-5.5"`, and on match does a
   single `sjson.SetBytes(body, "service_tier", "priority")` — failing open (returning the
   original body) on any non-match or `sjson` error.
4. `go.mod` requires `github.com/tidwall/sjson` and `gopkg.in/yaml.v3` in addition to the SDK.

## Full walkthrough: `claude-web-search-router` (only multi-file example)

Combines **ModelRouter** (`model.route`) and **Executor** (`executor.execute`/
`executor.execute_stream`) in one plugin. Intercepts Claude Code's built-in `web_search` tool
calls and answers them via one of several backends (Antigravity → Codex → xAI → Tavily fallback
chain by default, or pinned to a single backend via the `route` config field). Full source:
`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/claude-web-search-router/go/`.

File layout (mirror this structure for any nontrivial plugin):

| File | Responsibility |
|---|---|
| `main.go` | cgo boilerplate, `handleMethod`, config load/store, `pluginRegistration()`, top-level `routeModel`/`execute` |
| `detect.go` | Pure detection: is this a Claude web_search request? (`gjson`-based) |
| `model_resolve.go` | Per-backend target-model resolution |
| `fallback.go` | `defaultWebSearchFallbackChain()`, per-backend eligibility, `routeWithFallback` |
| `execution_fallback.go` | Ordered execution plans, `runOrderedExecutionPlans`, `host.model.*` calls |
| `execute_stream.go` | `executor.execute_stream` entrypoint — spawns a goroutine, returns immediately |
| `stream_forward.go` | Forwards a nested host model stream into the plugin's own outbound stream |
| `tavily.go` | Plain `net/http` client to a third-party search API, API-key round-robin |
| `claude_response.go` | Hand-builds Claude Messages JSON/SSE from search hits |
| `penalty.go` | In-memory backend penalty scoring (package-level `sync.Mutex`-guarded map) |

Each non-test file has a matching `*_test.go` — a reasonable structure to mirror for any
nontrivial plugin (unit-test per concern, not just end-to-end).

`go.mod` extra dependencies beyond the SDK: `github.com/tidwall/gjson`, `gopkg.in/yaml.v3`
(`github.com/sirupsen/logrus`, `github.com/tidwall/{match,pretty}`, `golang.org/x/sys` are
transitive/indirect).

Two things worth calling out explicitly:
- **`internal/registry` import**: `model_resolve.go` imports
  `github.com/router-for-me/CLIProxyAPI/v7/internal/registry`. This only works because the
  example lives *inside* the CLIProxyAPI repo and its `go.mod` has a local `replace` to the repo
  root. **A real out-of-tree plugin cannot do this** — `internal/*` is not part of the supported
  import surface; stick to `sdk/pluginabi` + `sdk/pluginapi`.
- **Hand-built Claude Messages SSE** (`claude_response.go`): when synthesizing a response from a
  non-streaming third-party API (Tavily) rather than forwarding a real model stream, the plugin
  must emit the exact Claude Messages event sequence itself: `message_start` →
  `content_block_start` (`server_tool_use`) → `content_block_delta` (`input_json_delta`) →
  `content_block_stop` → `content_block_start` (`web_search_tool_result`) → `content_block_stop`
  → `content_block_start` (`text`) → `content_block_delta` (`text_delta`) → `content_block_stop`
  → `message_delta` → `message_stop`, each event formatted as
  `fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, jsonData)`. If bridging protocols with a
  *real* host model stream instead, validate the exit protocol on the first chunk before
  forwarding (`looksLikeOpenAIResponsesSSE` sniffs and fails loudly rather than forwarding
  malformed protocol bytes downstream).
