# ABI Contract — Entrypoint, Structs, Envelope, Lifecycle

Pinned to upstream v7.2.88 (`sdk/pluginabi/types.go`, `sdk/pluginapi/types.go`,
`internal/pluginhost/loader_unix.go`, `internal/pluginhost/abi.go`).

## 1. What this ABI is

A CLIProxyAPI plugin is a native shared library (`.so`/`.dylib`/`.dll`) loaded with `dlopen`
(POSIX) or the Windows equivalent. There is no gRPC, no subprocess, no separate process — the
plugin runs in-process as a dynamically loaded C ABI library. Communication is a **synchronous
call-in / call-back RPC over raw byte buffers carrying JSON**:

- Host → Plugin: host calls the plugin's exported `call` function pointer with a `method` string
  and a JSON request body; the plugin returns a JSON envelope.
- Plugin → Host: the plugin uses a host-provided function pointer
  (`cliproxy_host_api.call`) to invoke `host.*` RPC methods — same buffer/envelope convention.

The C ABI never passes Go interfaces, slices, maps, channels, `context.Context`, or Go errors —
only `(method string, JSON bytes)` in, `(JSON bytes)` out. This makes the ABI language-neutral:
Go, C, Rust, or any language that can export `extern "C"` functions from a shared library can be
a plugin author.

## 2. Required exported symbol

Every plugin shared library must export exactly one C symbol, looked up via `dlsym`:

```c
cliproxy_plugin_init
```

Signature (from `internal/pluginhost/loader_unix.go`):

```c
typedef int (*cliproxy_plugin_init_fn)(const cliproxy_host_api*, cliproxy_plugin_api*);
```

If this symbol is missing, `Open()` fails immediately:
```go
return nil, fmt.Errorf("missing cliproxy_plugin_init: %s", dlerrorString())
```

## 3. The two C structs that cross the boundary

```c
typedef struct {
    void* ptr;
    size_t len;
} cliproxy_buffer;

/* passed host -> plugin at init time (already filled in by host) */
typedef int  (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
    uint32_t abi_version;
    void* host_ctx;                  /* opaque token identifying this plugin instance to the host */
    cliproxy_host_call_fn call;      /* plugin calls this to invoke host.* RPC methods */
    cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

/* the plugin must fill this in inside cliproxy_plugin_init */
typedef int  (*cliproxy_plugin_call_fn)(const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
    uint32_t abi_version;
    cliproxy_plugin_call_fn call;         /* host calls this for every RPC method */
    cliproxy_plugin_free_fn free_buffer;  /* host calls this to free memory `call` allocated */
    cliproxy_plugin_shutdown_fn shutdown; /* host calls this once, at unload */
} cliproxy_plugin_api;
```

So the plugin → host table (`cliproxy_plugin_api`, populated by the plugin in
`cliproxy_plugin_init`) has three functions: `call`, `free_buffer`, `shutdown`. The host → plugin
table (`cliproxy_host_api`, filled in by the host and passed in) has two functions plus a
version and an opaque context pointer: `call`, `free_buffer`.

## 4. `cliproxy_plugin_init` contract

Reference implementation, `examples/plugin/simple/go/main.go`:

```go
//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
    if plugin == nil {
        return 1
    }
    plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)
    plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
    plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
    plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
    return 0
}
```

The plugin must:

1. Set `plugin->abi_version = pluginabi.ABIVersion` (currently `1`). The host rejects the plugin
   if this doesn't match its own `pluginHostABIVersion` (`internal/pluginhost/abi.go`):
   ```go
   if uint32(client.api.abi_version) != pluginHostABIVersion {
       client.Shutdown()
       return nil, fmt.Errorf("plugin ABI version %d is not supported", uint32(client.api.abi_version))
   }
   ```
2. Populate `call` and `free_buffer` (non-nil, or the host rejects it:
   `"plugin function table is incomplete"`). `shutdown` is optional (may be nil), but every
   shipped example sets it anyway (a no-op is fine: `static void plugin_shutdown(void) {}`) — the
   loader unconditionally calls it during teardown if set.
3. Return `0` on success; any non-zero return code fails the load
   (`"cliproxy_plugin_init returned %d"`).

The host stashes its own callback identity into `host_ctx` — an opaque `uintptr_t` token
registered in a `sync.Map` (`hostCallbackEntries`), keyed to the specific `*Host` and plugin file
ID. The plugin must pass this same `host_ctx` back unchanged on every `call` invocation into
`cliproxy_host_api.call`; it demultiplexes which plugin instance is calling back in. Never
dereference or interpret `host_ctx` — treat it as opaque.

### The four exported symbols a plugin binary provides in total

| Symbol | Purpose |
|---|---|
| `cliproxy_plugin_init` | Required. Called once at dlopen; negotiates ABI version and fills the function table. |
| `cliproxyPluginCall` | The RPC dispatcher — host invokes this (via the function pointer wired in `init`) for every method, keyed by `method` string. |
| `cliproxyPluginFree` | Frees a response buffer the plugin allocated (host calls this after copying bytes out). |
| `cliproxyPluginShutdown` | Called once when the host unloads the plugin. |

Symbol names for `cliproxyPluginCall/Free/Shutdown` are not hard-mandated by the loader — the
loader only looks up `cliproxy_plugin_init` by name and gets the other three via the function
pointers the plugin sets inside it. All shipped examples use this exact naming; it's the de facto
convention.

## 5. Host → plugin call path (`Call`)

`internal/pluginhost/loader_unix.go`, `(*dynamicLibraryClient).Call`:

```go
func (c *dynamicLibraryClient) Call(ctx context.Context, method string, request []byte) ([]byte, error) {
    ...
    cMethod := C.CString(method)
    ...
    var response C.cliproxy_buffer
    rc := C.cliproxy_call_plugin(c.api.call, cMethod, (*C.uint8_t)(cRequest), C.size_t(len(request)), &response)
    var out []byte
    if response.ptr != nil && response.len > 0 {
        out = C.GoBytes(response.ptr, C.int(response.len))
    }
    if response.ptr != nil {
        c.api.free_buffer(response.ptr, response.len)   // host frees plugin-allocated memory
    }
    if rc != 0 {
        if isPluginErrorEnvelope(out) { return out, nil }  // structured error envelope, not a hard failure
        return nil, fmt.Errorf("plugin call %s returned %d: %s", method, int(rc), string(out))
    }
    return out, nil
}
```

Key detail: a non-zero return code `rc` is **not automatically fatal** — if the returned bytes
parse as a valid `pluginabi.Envelope{OK:false, Error:...}`, the host treats it as a normal
"handled but errored" RPC result and surfaces `Error.Code`/`Message` upstream, rather than
failing the whole plugin call path.

## 6. Plugin → host callback path (reverse direction)

`internal/pluginhost/host_callbacks_unix.go` exports `cliproxyHostCall`, which is exactly what
`cliproxy_host_api.call` points to. The plugin invokes it directly as a C function pointer (no
dlsym needed by the plugin — the pointer was handed to it in `cliproxy_plugin_init`):

```go
//export cliproxyHostCall
func cliproxyHostCall(hostCtx unsafe.Pointer, method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
    id := uintptr(*(*C.uintptr_t)(hostCtx))
    rawHost, _ := hostCallbackEntries.Load(id)
    entry := rawHost.(dynamicHostCallbackEntry)
    ...
    resp, errCall := entry.host.callFromPlugin(ctx, C.GoString(method), requestBytes)
    ...
}
```

So a plugin calling "the host" is really:
`host_api->call(host_api->host_ctx, "host.http.do", bytes, len, &buf)`.

## 7. ABI versioning

From `sdk/pluginabi/types.go`:

```go
const (
    // ABIVersion tracks the native C ABI shape (native plugin exports).
    ABIVersion uint32 = 1
    // SchemaVersion tracks the RPC JSON contract exchanged at plugin.register.
    // Increment only for breaking RPC changes. New capabilities such as ModelRouter
    // are gated by capability flags and method names while the version stays at 1.
    SchemaVersion uint32 = 1
)
```

`ABIVersion` gates whether `dlopen` even succeeds (hard mismatch = reject). `SchemaVersion` is
carried in the `plugin.register` payload and allows additive/backward-compatible evolution of the
JSON RPC contract without bumping `ABIVersion` — new capabilities are added via new boolean
capability flags and new method-name constants, not breaking changes.

## 8. The JSON RPC envelope

Every call in both directions (`method` + JSON `request` bytes → JSON `response` bytes) uses this
envelope, defined once in `sdk/pluginabi/types.go`:

```go
type Envelope struct {
    OK     bool            `json:"ok"`
    Result json.RawMessage `json:"result,omitempty"`
    Error  *Error          `json:"error,omitempty"`
}

type Error struct {
    Code       string `json:"code"`
    Message    string `json:"message"`
    Retryable  bool   `json:"retryable,omitempty"`
    HTTPStatus int    `json:"http_status,omitempty"`
}
```

Example success response body (raw bytes handed back through `cliproxy_buffer`):
```json
{"ok":true,"result":{"identifier":"plugin-example"}}
```
Example error:
```json
{"ok":false,"error":{"code":"unknown_method","message":"unknown method: foo.bar"}}
```

Both C and Rust reference plugins build these envelopes with tiny hand-rolled helpers rather than
a JSON library:

```c
static char* wrap_ok(const char* result_json) {
    return format_string("{\"ok\":true,\"result\":%s}", result_json == NULL ? "{}" : result_json);
}
static char* make_error(const char* code, const char* message) {
    char* escaped = json_escape(message);
    char* out = format_string("{\"ok\":false,\"error\":{\"code\":\"%s\",\"message\":\"%s\"}}", code, escaped == NULL ? "" : escaped);
    free(escaped);
    return out;
}
```

Neither reference plugin links a JSON library — that's a deliberate "zero external dependencies"
demo choice, **not a best practice to copy**. A real-world plugin should use a real JSON library
(`serde_json` in Rust, `cJSON`/`yyjson` in C, `encoding/json` in Go).

### Binary payloads travel as base64 inside JSON

Because the envelope is JSON text, any raw bytes (HTTP bodies, streamed chunks, request/response
payloads) are base64-encoded as JSON string fields — confirmed in
`examples/plugin/simple/README.md`: *"Raw byte fields are encoded as base64 by JSON."* Concretely:

- `executor.execute` result: `{"Payload": "<base64>", "Headers": {...}}`
- `executor.execute_stream` result: `{"headers": {...}, "chunks": [{"Payload": "<base64>"}]}`
- `management.handle` / `executor.http_request` result: `{"StatusCode":200,"Headers":{...},"Body":"<base64>"}`
- `command_line.execute` result: `{"Stdout":"<base64>","ExitCode":0}`

### Field-name casing caveat

Most `pluginapi` request/response structs (e.g. `ExecutorRequest`, `RequestTransformRequest`,
`AuthData`, `ModelInfo`) carry **no `json:` struct tags**. Go's `encoding/json` therefore
serializes them using the exact exported Go field name as the JSON key (e.g. `Model`, `Stream`,
`Provider`, `StorageJSON`, `AuthID`) — not `snake_case`. A minority of structs used specifically
for host-callback framing (`HostModelExecutionRequest`, `HostAuthGetRequest`,
`HostAuthFileEntry`, etc. — anything under the `Host*` prefix in `sdk/pluginapi/types.go`) **do**
carry explicit lower-`snake_case` json tags, e.g.:

```go
type HostModelExecutionRequest struct {
    EntryProtocol string `json:"entry_protocol"`
    ExitProtocol  string `json:"exit_protocol"`
    Model         string `json:"model"`
    Stream        bool   `json:"stream"`
    Body          []byte `json:"body"`
    Headers       http.Header `json:"headers"`
    Query         url.Values  `json:"query"`
    Alt           string `json:"alt"`
}
```

The top-level `registration`/`rpcRegistration` struct itself DOES have `json:` tags
(`schema_version`, `metadata`, `capabilities`) even though many nested payload structs don't. A
non-Go plugin implementer must check each struct individually — there is no single universal
casing rule across the whole SDK.

## 9. Lifecycle methods

| Constant | Value | Purpose |
|---|---|---|
| `MethodPluginRegister` | `plugin.register` | Called once after `cliproxy_plugin_init` succeeds. Plugin returns its `Metadata` + capability flags (the `registration` struct). |
| `MethodPluginReconfigure` | `plugin.reconfigure` | Called when the plugin's YAML config subtree (`plugins.configs.<pluginID>`) changes at runtime; plugin re-parses config and may return updated registration. |
| `MethodPluginShutdown` | `plugin.shutdown` | Sent before the C-level `shutdown()` export is invoked (graceful drain signal). |

`plugin.register`/`plugin.reconfigure` response shape (`internal/pluginhost/rpc_schema.go`
`rpcRegistration`, matching the example's local `registrationCapability`):

```json
{
  "schema_version": 1,
  "metadata": { "name": "...", "version": "...", "author": "...", "github_repository": "...", "logo": "...", "config_fields": [...] },
  "capabilities": { "model_registrar": true, "executor": true, "executor_model_scope": "both", "executor_input_formats": ["chat-completions"], ... }
}
```

`Metadata` (`sdk/pluginapi/types.go`):

```go
type Metadata struct {
    Name             string          // stable human-readable plugin name
    Version          string          // plugin release version
    Author           string          // plugin author or organization
    GitHubRepository string          // repository URL for source and support
    Logo             string          // display asset reference for management clients
    ConfigFields     []ConfigField   // plugin-owned configuration fields for management clients
}

type ConfigField struct {
    Name        string          // key under plugins.configs.<pluginID>
    Type        ConfigFieldType // string|number|integer|boolean|enum|array|object
    EnumValues  []string        // allowed values when Type == enum
    Description string
}
```

`ConfigField` is metadata only — declaring a config field does not auto-bind or validate it; the
plugin must parse the raw YAML subtree itself on `plugin.reconfigure`. `Priority` and `Enabled`
are host-owned (`plugins.configs.<pluginID>.priority`/`.enabled` in host YAML), not plugin-
reported — they are surfaced back to the plugin via `plugin.reconfigure`, not declared in
`plugin.register`.

## 10. Loading, discovery, and failure modes

- The host searches `plugins/<GOOS>/<GOARCH>` then `plugins` for accepted extensions: `.so`
  (Linux/FreeBSD), `.dylib` (macOS), `.dll` (Windows).
- Plugin IDs must match `[A-Za-z0-9][A-Za-z0-9._-]{0,127}` and are derived from the dynamic
  library's basename without the platform extension (e.g. `simple-rust.dylib` → plugin ID
  `simple-rust`).
- Dynamic plugins are disabled by default; must be turned on:
  ```yaml
  plugins:
    enabled: true
    dir: "plugins"
    configs:
      simple-rust:
        enabled: true
        priority: 1
  ```
- Loading sequence (`dynamicLibraryLoader.Open`):
  1. `dlopen(path, RTLD_NOW | RTLD_LOCAL)`.
  2. `dlsym(handle, "cliproxy_plugin_init")` — missing symbol → load error.
  3. Host allocates a `cliproxy_host_api` (C-heap `malloc`) and a small `host_ctx` token, wires
     `call`/`free_buffer` to its own exported cgo functions, then calls
     `cliproxy_call_init(initSymbol, hostAPI, &client.api)`.
  4. Checks `client.api.abi_version == pluginHostABIVersion` — mismatch is a hard load failure.
  5. Checks `client.api.call != nil && client.api.free_buffer != nil` — incomplete function table
     is also a hard load failure.
  6. On any failure the host calls `Shutdown()` on the partially-built client, which calls the
     plugin's `shutdown()` (if set), frees host-side allocations, and `dlclose`s the handle.

### Per-call error semantics

- `call()` returning `0` means "I produced a well-formed envelope in `response`" — regardless of
  whether that envelope is `{"ok":true,...}` or `{"ok":false,"error":{...}}`.
- A non-zero return is checked by the host: if the response body still parses as a valid
  `{"ok":false,"error":{...}}` envelope (`isPluginErrorEnvelope`), the host accepts it as a normal
  error response rather than a transport failure. Otherwise it's surfaced as a Go `error`:
  `"plugin call %s returned %d: %s"`.
- Practically: always return `0` and put failures inside the JSON envelope's `error` field unless
  you truly cannot produce any JSON at all (e.g. `method == NULL`).

## 11. Trust boundary

Directly from `examples/plugin/simple/README.md`:

> "Standard dynamic library plugins are trusted in-process code. Panic recovery can protect
> host-managed calls, but it cannot prevent a plugin from exiting the process, corrupting memory,
> mutating global process state, or leaking secrets. Install only plugins you trust as much as the
> service binary."

A crash, segfault, or `abort()` inside your plugin's `call()` takes down the entire host process
— there is no process isolation. Panics in Rust code that unwind across the `extern "C"` boundary
are undefined behavior unless caught; wrap `plugin_call` in `std::panic::catch_unwind` and convert
panics into `{"ok":false,"error":{...}}` envelopes.
