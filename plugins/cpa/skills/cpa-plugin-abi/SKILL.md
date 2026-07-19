---
name: cpa-plugin-abi
description: Use when building or debugging a CLIProxyAPI native dynamic-library plugin (Go/C/Rust) and you need the C ABI contract — cliproxy_plugin_init, the call/free_buffer/shutdown function tables, the {ok,result,error} JSON-RPC envelope, the plugin.register/reconfigure/shutdown lifecycle, the ~35 method-name constants, the host.* callback surface, or the memory-ownership/free_buffer rules.
---

# CLIProxyAPI Plugin ABI

CLIProxyAPI plugins are native shared libraries (`.so`/`.dylib`/`.dll`) loaded with `dlopen`.
There is no gRPC, no subprocess — the plugin runs in-process. Host and plugin talk through a
**synchronous call-in/call-back RPC over raw byte buffers carrying JSON**. Everything below is
pinned to upstream **v7.2.88** (`sdk/pluginabi/types.go`, `sdk/pluginapi/types.go`).

## The one required export

Every plugin binary exports exactly one C symbol, resolved via `dlsym`:

```c
int cliproxy_plugin_init(const cliproxy_host_api* host, cliproxy_plugin_api* plugin);
```

`host` is filled in and owned by the host; `plugin` is an out-parameter the plugin must
populate before returning. Minimum required work inside it:

```go
plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)   // must equal 1, or the host rejects the load
plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)   // optional, but set it (no-op is fine)
return 0   // any non-zero return aborts loading
```

`abi_version` must equal `pluginabi.ABIVersion` (currently `1`) exactly, or the host refuses to
load the plugin (`"plugin ABI version %d is not supported"`). `call` and `free_buffer` must be
non-nil or the host rejects it (`"plugin function table is incomplete"`). The host also stashes
an opaque `host_ctx` token in `cliproxy_host_api.host_ctx` — pass it back unchanged on every
callback into `host_api->call`.

Full struct layouts, loading sequence, and failure modes:
`${CLAUDE_PLUGIN_ROOT}/skills/cpa-plugin-abi/references/abi-contract.md`

## The `cliproxy_buffer` struct

```c
typedef struct { void* ptr; size_t len; } cliproxy_buffer;
```

Used as the out-parameter for every response in both directions. Length is authoritative — the
buffer is **not** NUL-terminated. See memory rules below.

## The JSON-RPC envelope

Every `call()` — in both directions — takes `(method string, request []byte)` and returns one of:

```json
{"ok": true, "result": { ... }}
{"ok": false, "error": {"code": "unknown_method", "message": "..."}}
```

`sdk/pluginabi/types.go`: `Envelope{OK bool; Result json.RawMessage; Error *Error}`,
`Error{Code, Message string; Retryable bool; HTTPStatus int}`. A `call()` return code of `0`
means "well-formed envelope was produced" — even `{"ok":false,...}` counts. Reserve non-zero
returns for "could not construct any JSON at all." Full envelope details, base64 payload
convention, and field-casing caveats (most `pluginapi` structs have **no** `json:` tag →
PascalCase keys; `Host*` types **do** use `snake_case` tags):
`${CLAUDE_PLUGIN_ROOT}/skills/cpa-plugin-abi/references/abi-contract.md`

## Lifecycle

1. Host `dlopen`s the library, resolves `cliproxy_plugin_init`, calls it once.
2. Host calls `plugin.register` (`pluginabi.MethodPluginRegister`) — plugin returns `Metadata` +
   capability boolean flags (the wire `registration` struct).
3. Host calls `plugin.reconfigure` (`MethodPluginReconfigure`) whenever the plugin's YAML config
   subtree (`plugins.configs.<pluginID>`) changes — plugin re-parses and may re-report registration.
4. Host calls `plugin.shutdown` (`MethodPluginShutdown`) as a graceful drain signal, then invokes
   the C-level `plugin->shutdown()` export at unload.

## Method-name constants (~35 total)

All are `pluginabi.Method*` string constants dispatched by exact match in your `call()`. Grouped:
lifecycle (`plugin.register`/`reconfigure`/`shutdown`), model discovery (`model.register`,
`model.static`, `model.for_auth`), auth provider (`auth.identifier`/`parse`/`login.start`/
`login.poll`/`refresh`), frontend auth (`frontend_auth.*`), scheduling/routing
(`scheduler.pick`, `model.route`), executor (`executor.identifier`/`execute`/`execute_stream`/
`count_tokens`/`http_request`), request/response transform (`request.translate`/`normalize`/
`intercept_before`/`intercept_after`, `response.translate`/`normalize_before`/`normalize_after`/
`intercept_after`/`intercept_stream_chunk`), thinking (`thinking.identifier`/`apply`), usage
(`usage.handle`), CLI (`command_line.register`/`execute`), management
(`management.register`/`handle`), and 15 `host.*` reverse-direction callbacks. Full table with
request/response type names: `${CLAUDE_PLUGIN_ROOT}/skills/cpa-plugin-abi/references/rpc-methods.md`

## Host callback surface (plugin → host)

The plugin calls back into the host via the function pointer handed to it at init:
`host_api->call(host_api->host_ctx, "host.http.do", bytes, len, &response)`. Four families:
`host.http.*` (do, do_stream, stream_read, stream_close — issue upstream HTTP under host
transport/request-log policy; **never dial out directly** if you want request-log capture),
`host.model.*` (execute, execute_stream, stream_read, stream_close — delegate back through the
host's own model pipeline), `host.stream.*` (emit, close — push chunks into a host-managed
outbound stream), `host.auth.*` (list, get, get_runtime, save — credential file access), and
`host.log`. When forwarding a `host_callback_id` from a host-invoked context (e.g.
`management.handle`) into `host.model.*`, the host uses it to skip that plugin's own
interceptors on the nested call and avoid recursion. Full per-method request/response shapes:
`${CLAUDE_PLUGIN_ROOT}/skills/cpa-plugin-abi/references/host-callbacks.md`

## Memory ownership — get this wrong and you corrupt the host's heap

Symmetric, C-heap, manual, per-direction:

- **Plugin responds to host `call()`**: plugin allocates the response buffer; host copies bytes
  out (`C.GoBytes`); host frees by calling **the plugin's own** `plugin_api.free_buffer(ptr, len)`
  — never `free()` directly.
- **Host responds to plugin's `host_api->call()`**: host allocates (`C.CBytes`); plugin copies
  what it needs; plugin frees by calling **the host's** `host_api->free_buffer(ptr, len)`.
- Always heap-allocate with the C allocator (`malloc`/`C.CBytes`/Rust `Vec` + `mem::forget`) —
  never hand a GC-managed pointer across the boundary.

Full ownership table, Rust `Vec::from_raw_parts` gotcha, and the "non-zero rc but valid error
envelope is not a hard failure" rule:
`${CLAUDE_PLUGIN_ROOT}/skills/cpa-plugin-abi/references/memory-model.md`

## Ground truth

- `${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginabi-types.go` — method constants, `Envelope`, `Error`.
- `${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginapi-types.go` — every request/response struct.
- `${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/simple/go/main.go` — reference Go plugin (all capabilities).
- `${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/simple/c/src/plugin.c`, `examples/simple/rust/src/lib.rs` — non-Go reference plugins.
- `${CLAUDE_PLUGIN_ROOT}/references/upstream/docs-plugin/host-callbacks.md` — prose host-callback spec.
