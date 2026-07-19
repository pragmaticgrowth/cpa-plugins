# Annotated canonical Go plugin skeleton

Pinned to upstream **v7.2.88**. Every fact below is transcribed from vendored source, not
inferred: `${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/simple/go/main.go` and
`${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginapi-types.go` /
`pluginabi-types.go`.

## File anatomy

A plugin's `main.go` has five parts in this order: (1) `package main` + cgo preamble comment +
`import "C"`, (2) Go-side imports, (3) local types (envelope, registration/capability mirrors),
(4) the four `//export`ed functions plus `func main() {}`, (5) `handleMethod` and its handler
functions plus the envelope/buffer helpers.

## 1. cgo preamble — copied verbatim, never edited

```go
package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);
*/
import "C"
```

`cliproxy_buffer` is a `(ptr, len)` pair — the *only* shape that crosses the ABI in either
direction. `cliproxy_host_api` is the vtable the host hands the plugin at init (its own
`host_ctx` opaque pointer plus `call`/`free_buffer` function pointers, used only if the plugin
calls back into the host). `cliproxy_plugin_api` is the vtable the plugin fills in and hands back
to the host, naming the plugin's own `call`/`free_buffer`/`shutdown` functions.

If the plugin needs to call the host (`host.http.*`, `host.model.*`, `host.auth.*`,
`host.stream.*`), add this C helper block inside the same comment, after the struct
declarations:

```c
static const cliproxy_host_api* stored_host;

static void store_host_api(const cliproxy_host_api* host) {
	stored_host = host;
}

static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (stored_host == NULL || stored_host->call == NULL) {
		return 1;
	}
	return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static void free_host_buffer(void* ptr, size_t len) {
	if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
		stored_host->free_buffer(ptr, len);
	}
}
```

`stored_host` is package-level C state set once, inside `cliproxy_plugin_init`, via
`C.store_host_api(host)`. This lets Go code call `C.call_host_api(...)` later without threading a
context pointer through every call site. See `references/real-world-patterns.md` for the Go-side
`callHost` wrapper.

## 2. Go-side imports

Minimal (no host callbacks, no SDK — hand-rolled style, not recommended):

```go
import (
	"encoding/json"
	"unsafe"
)
```

Recommended (SDK-typed style — type-safe constants and structs):

```go
import (
	"encoding/json"
	"net/http"
	"unsafe"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)
```

The SDK is optional — the wire protocol is plain JSON with exported-Go-field-name keys
(PascalCase, no `json:` tags on most `pluginapi` structs), and four of the five official upstream
examples skip the SDK entirely, dispatching on bare string literals (`"plugin.register"`) and
building hand-written JSON strings. That proves the contract, but **always use the SDK types**
(`pluginabi.MethodXxx` constants, `pluginapi.Xxx` structs) in anything beyond a throwaway demo —
compile-time field checking, and you inherit wire-format fixes on SDK version bumps.

## 3. `go.mod` — exact content, pinned to v7.2.88

```
module github.com/you/my-plugin

go 1.26.0

require github.com/router-for-me/CLIProxyAPI/v7 v7.2.88
```

- Module path is yours to choose; it need not live under `router-for-me/CLIProxyAPI`.
- `go 1.26.0` matches the host repo's own toolchain version — pin to whatever the CLIProxyAPI
  root `go.mod` uses to avoid build mismatches.
- `require github.com/router-for-me/CLIProxyAPI/v7 v7.2.88` resolves the published SDK module at
  the pinned upstream tag. (The in-tree upstream examples instead pin a placeholder
  `v7.0.0` + a `replace ... => ../../../..` directive, because they build *inside* the
  CLIProxyAPI monorepo against the local checkout — drop the `replace` line entirely for a real
  out-of-tree plugin repo; let `require` resolve the tagged module.)
- No other dependency is required for a plugin using only `sdk/pluginabi` + `sdk/pluginapi` — the
  entire ABI layer is standard library (`encoding/json`, `net/http`, `unsafe`) plus those two SDK
  packages. Add third-party deps (`github.com/tidwall/sjson`, `github.com/tidwall/gjson`,
  `gopkg.in/yaml.v3`) only as your own logic needs them — see
  `references/real-world-patterns.md`.
- **`internal/*` CLIProxyAPI packages are off-limits.** They only work for in-tree examples using
  a local `replace` to the monorepo root. A real external plugin building against the published
  SDK module cannot import `internal/...` — stick to `sdk/pluginabi` and `sdk/pluginapi`.

Scaffold instead of hand-writing: `/cpa:scaffold` generates this file from
`${CLAUDE_PLUGIN_ROOT}/templates/go-plugin/go.mod.tmpl`, which already pins `v7.2.88`.

## 4. The wire envelope

```go
type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
```

This mirrors the SDK's own `pluginabi.Envelope`/`pluginabi.Error` (which additionally carries
`Retryable bool` and `HTTPStatus int` on the error, both `omitempty`). Success on the wire:
`{"ok":true,"result":{...}}`. Failure: `{"ok":false,"error":{"code":"...","message":"..."}}`.

```go
func okEnvelope(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{OK: true, Result: raw})
}

func errorEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
	return raw
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw) // C-heap allocation; the host frees it via cliproxyPluginFree.
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}
```

## 5. The four exported symbols

Fixed names, `//export`-annotated, resolved by the host by exact symbol name after `dlopen`. A
typo or rename silently breaks plugin loading.

```go
func main() {} // required by -buildmode=c-shared; never invoked by the host

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1 // non-zero == failure; host will not load this plugin
	}
	// C.store_host_api(host) // only if this plugin calls back into the host
	plugin.abi_version = C.uint32_t(pluginabi.ABIVersion) // must equal the host's ABI version (1)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0 // success
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required"))
		return 1
	}
	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, err := handleMethod(C.GoString(method), requestBytes)
	if err != nil {
		writeResponse(response, errorEnvelope("plugin_error", err.Error()))
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, length C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {}
```

Return-code convention: `0` on success, non-zero on failure — but `cliproxyPluginCall` still
writes a JSON error envelope into `response` even on failure. The `envelope.ok` boolean is the
authoritative success signal; the `C.int` return is a secondary/fast-path signal.

Buffer ownership is symmetric-by-direction: the plugin allocates response buffers with
`C.CBytes` (C `malloc`) and the host calls `cliproxyPluginFree` (plain `C.free`) once it has
consumed them. **Never let Go's GC own this memory** — it's on the C heap.

## 6. `handleMethod` — the dispatch table

```go
func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		return okEnvelope(exampleRegistration())
	case pluginabi.MethodModelStatic, pluginabi.MethodModelForAuth:
		return okEnvelope(pluginapi.ModelResponse{Provider: "my-plugin", Models: exampleModels()})
	default:
		// Unknown methods are NOT a Go error — they're a normal ok:false envelope.
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}
```

Add one `case` per `pluginabi.MethodXxx` constant for every capability this plugin declares
`true` in its registration. Reserve the Go `error` return for cases where you literally cannot
build a response (e.g. JSON unmarshal failure) — an unknown/unsupported method, invalid input, or
declined routing decision should all still produce a normal envelope (`ok:false` or
`Handled:false`), not a Go error.

The full RPC method catalogue (every `pluginabi.MethodXxx` constant, verbatim) lives in
`${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginabi-types.go`; cross-reference against
`${CLAUDE_PLUGIN_ROOT}/skills/cpa-plugin-abi/` for the ABI-level contract and
`${CLAUDE_PLUGIN_ROOT}/skills/cpa-capabilities/` for what each capability's request/response
shape means operationally.

## 7. `plugin.register` / `plugin.reconfigure` — required Metadata fields + capability flags

Both methods share one handler and return the same shape:

```go
type registration struct {
	SchemaVersion uint32                 `json:"schema_version"`
	Metadata      pluginapi.Metadata     `json:"metadata"`
	Capabilities  registrationCapability `json:"capabilities"`
}
```

`SchemaVersion` is always `pluginabi.SchemaVersion` (currently `1`) — new capabilities are gated
by capability flags and method names, not schema-version bumps.

`pluginapi.Metadata` — every field, exactly as declared in `sdk/pluginapi/types.go`:

```go
type Metadata struct {
	Name             string        // stable human-readable plugin name — MUST match the
	                                // config key (plugins.configs.<Name>) and library filename
	Version          string        // plugin release version
	Author           string        // author/org
	GitHubRepository string        // repo URL for source/support
	Logo             string        // display asset reference for management clients (optional)
	ConfigFields     []ConfigField // plugin-owned config fields, descriptive only — see below
}

type ConfigFieldType string
const (
	ConfigFieldTypeString  ConfigFieldType = "string"
	ConfigFieldTypeNumber  ConfigFieldType = "number"
	ConfigFieldTypeInteger ConfigFieldType = "integer"
	ConfigFieldTypeBoolean ConfigFieldType = "boolean"
	ConfigFieldTypeEnum    ConfigFieldType = "enum"
	ConfigFieldTypeArray   ConfigFieldType = "array"
	ConfigFieldTypeObject  ConfigFieldType = "object"
)

type ConfigField struct {
	Name        string          // config key under plugins.configs.<pluginID>
	Type        ConfigFieldType
	EnumValues  []string        // allowed values when Type == ConfigFieldTypeEnum
	Description string
}
```

`ConfigFields` is **descriptive only** — it drives the management UI/API's rendering of your
plugin's config, but the host does not validate or pre-parse values against it. The plugin
receives its config as raw YAML bytes and owns the schema end-to-end (see
`references/real-world-patterns.md` §Config parsing).

**Capabilities** — the wire-level `registrationCapability` struct is a **local, snake_case-tagged
mirror** of the host's own `pluginapi.Capabilities` (which uses interface-typed fields
internally; the plugin declares itself with plain bools/strings/slices instead). Full mirror,
exactly as used by `examples/simple/go/main.go`:

```go
type registrationCapability struct {
	ModelRegistrar           bool                         `json:"model_registrar"`
	ModelProvider            bool                         `json:"model_provider"`
	AuthProvider             bool                         `json:"auth_provider"`
	FrontendAuthProvider     bool                         `json:"frontend_auth_provider"`
	Executor                 bool                         `json:"executor"`
	ExecutorModelScope       pluginapi.ExecutorModelScope `json:"executor_model_scope"`
	ExecutorInputFormats     []string                     `json:"executor_input_formats,omitempty"`
	ExecutorOutputFormats    []string                     `json:"executor_output_formats,omitempty"`
	RequestTranslator        bool                         `json:"request_translator"`
	RequestNormalizer        bool                         `json:"request_normalizer"`
	ResponseTranslator       bool                         `json:"response_translator"`
	ResponseBeforeTranslator bool                         `json:"response_before_translator"`
	ResponseAfterTranslator  bool                         `json:"response_after_translator"`
	ThinkingApplier          bool                         `json:"thinking_applier"`
	UsagePlugin              bool                         `json:"usage_plugin"`
	CommandLinePlugin        bool                         `json:"command_line_plugin"`
	ManagementAPI            bool                         `json:"management_api"`
}
```

Two capabilities present on the host-side `pluginapi.Capabilities` struct but not in this mirror
(add them yourself if needed): `Scheduler bool` (`"scheduler"`, for `scheduler.pick`),
`ModelRouter bool` (`"model_router"`, for `model.route` — see
`examples/claude-web-search-router/go/main.go`'s `registrationCapability`), and
`FrontendAuthProviderExclusive bool` (`"frontend_auth_provider_exclusive"` — makes a frontend
auth provider the *only* active request auth provider when selected).

Only set the flags you actually implement `true`; the rest default to `false`/zero and the host
simply never calls those RPC methods on this plugin.

`ExecutorModelScope` (only meaningful when `Executor: true`):

```go
type ExecutorModelScope string
const (
	ExecutorModelScopeBoth   ExecutorModelScope = "both"   // static + OAuth auth-bound models
	ExecutorModelScopeStatic ExecutorModelScope = "static" // non-OAuth static models only
	ExecutorModelScopeOAuth  ExecutorModelScope = "oauth"  // OAuth auth-bound models only
)
```

Empty defaults to `"both"`. **Executor plugins must declare both `executor_input_formats` and
`executor_output_formats`** — the host passes requests through untranslated when the client
protocol matches a declared format; otherwise it translates the inbound request into one declared
input format and translates the executor's response back to the client protocol. Recognized
format names: `chat-completions`, `responses`, `claude`, plus legacy aliases `openai`,
`openai-response` for Chat Completions / Responses respectively.

Plugin priority (which plugin runs first when several are eligible) is set in host config
(`plugins.configs.<id>.priority`), never in the plugin binary — the host's rule is "native logic
wins, plugins fill gaps, higher-priority plugins run before lower-priority plugins."

`exampleModels()` — a `pluginapi.ModelInfo` populated minimally (most fields optional, left zero):

```go
func exampleModels() []pluginapi.ModelInfo {
	return []pluginapi.ModelInfo{{
		ID:                         "my-plugin-model",
		Object:                     "model",
		OwnedBy:                    "my-plugin",
		DisplayName:                "My Plugin Model",
		SupportedGenerationMethods: []string{"chat"},
		ContextLength:              8192,
		MaxCompletionTokens:        1024,
		UserDefined:                true,
	}}
}
```

## 8. Build

```bash
mkdir -p plugins/darwin/$(go env GOARCH)   # or plugins/linux/<arch>, plugins/windows/<arch>
go build -buildmode=c-shared -o plugins/darwin/$(go env GOARCH)/my-plugin.dylib .
rm -f plugins/darwin/$(go env GOARCH)/my-plugin.h
```

- `-buildmode=c-shared` is mandatory — it makes `//export`-annotated functions callable as C
  symbols and triggers cgo's `.h` generation, which the build immediately deletes (the ABI is
  defined by the fixed C structs in the preamble, not by a generated header).
- Output convention: `plugins/<GOOS>/<GOARCH>/<pluginID>.<so|dylib|dll>` (`.so` Linux/FreeBSD,
  `.dylib` macOS, `.dll` Windows). The host searches `plugins/<GOOS>/<GOARCH>` first, then falls
  back to flat `plugins/`.
- **Plugin ID = library basename without extension**, must match
  `[A-Za-z0-9][A-Za-z0-9._-]{0,127}`, and is the key used everywhere: host config
  (`plugins.configs.<pluginID>`), Management API (`/v0/management/plugins/{pluginID}`,
  `/v0/resource/plugins/{pluginID}/...`).

See `${CLAUDE_PLUGIN_ROOT}/skills/cpa-build-and-wire/` for full build/wire/config details and the
`/cpa:scaffold`/`/cpa:build`/`/cpa:wire` command flow.

## 9. Host config to enable a plugin

Dynamic plugins are **disabled by default**.

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    my-plugin:
      enabled: true
      priority: 1
      # ...your declared ConfigFields go here as plain YAML...
```

`plugins.configs.<pluginID>` is handed to `plugin.register`/`plugin.reconfigure` as normalized
YAML bytes inside the JSON request (`{"config_yaml": <bytes>}`) — the YAML key **must match the
plugin's registered `Metadata.Name` exactly**.

## Must-know gotchas (condensed)

1. Four exported symbols, fixed names, never renamed.
2. Everything crossing the ABI is `(pointer, length)` raw JSON — no Go types cross directly.
3. `[]byte` fields serialize as base64 on the wire (`AuthData.StorageJSON`,
   `ExecutorResponse.Payload`, `ExecutorStreamChunk.Payload`, `ExecutorHTTPResponse.Body`, …).
4. You allocate response buffers with `C.CBytes`; free with `C.free` — never mix with Go's GC.
5. Executor requires both `executor_input_formats` and `executor_output_formats`.
6. Package-level state must be concurrency-safe (one shared-library instance, multi-goroutine
   calls).
7. Fully trusted, in-process, unsandboxed — a crash or panic here can take down the host.
8. The SDK is optional but strongly recommended over hand-rolled JSON strings.
9. `plugin.register`/`plugin.reconfigure` share one handler; write `configure()` to fully replace
   prior state on every call, not partially merge.
10. Build with `-buildmode=c-shared`, delete the generated `.h`, place under
    `plugins/<GOOS>/<GOARCH>/<pluginID>.<ext>`.
