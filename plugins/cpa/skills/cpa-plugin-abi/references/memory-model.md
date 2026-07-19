# Memory Model — Ownership, `free_buffer`, and Buffer Conventions

This is a **manually-managed, two-directional heap-ownership handoff**. There is no shared
allocator between the Go host and a non-Go plugin, so ownership must cross the boundary through
an explicit "who allocated it, who frees it" protocol using the `free_buffer` function pointer in
each direction's function table. Pinned to upstream v7.2.88
(`internal/pluginhost/loader_unix.go`, `internal/pluginhost/host_callbacks_unix.go`,
`examples/plugin/simple/c/src/plugin.c`, `examples/plugin/simple/rust/src/lib.rs`).

## The `cliproxy_buffer` struct

```c
typedef struct {
    void* ptr;
    size_t len;
} cliproxy_buffer;
```

Used as the out-parameter for every response in both directions. **`len` is authoritative — the
buffer is not NUL-terminated.** Never assume a NUL-terminated C string on either side of this
ABI; always carry length explicitly.

## Direction 1: plugin → host (the normal `call` path)

**Rule: the plugin allocates the response buffer; the host copies it out; the host then asks the
plugin to free its own buffer via `plugin->free_buffer`.** The plugin must never free the buffer
itself after returning it, and the host must never call `free()`/libc free directly on plugin
memory (different allocators, different runtimes, possibly different C libraries statically
linked into the `.so` — that would be undefined behavior).

Ground truth, `internal/pluginhost/loader_unix.go` (`dynamicLibraryClient.Call`):

```go
func (c *dynamicLibraryClient) Call(ctx context.Context, method string, request []byte) ([]byte, error) {
    ...
    var response C.cliproxy_buffer
    rc := C.cliproxy_call_plugin(c.api.call, cMethod, (*C.uint8_t)(cRequest), C.size_t(len(request)), &response)
    var out []byte
    if response.ptr != nil && response.len > 0 {
        out = C.GoBytes(response.ptr, C.int(response.len))       // host COPIES bytes into Go-managed memory
    }
    if response.ptr != nil {
        C.cliproxy_free_plugin_buffer(c.api.free_buffer, response.ptr, response.len)  // host asks PLUGIN to free its own buffer
    }
    if rc != 0 {
        if isPluginErrorEnvelope(out) {
            return out, nil     // non-zero rc + a well-formed {"ok":false,...} body is NOT a hard failure
        }
        return nil, fmt.Errorf("plugin call %s returned %d: %s", method, int(rc), string(out))
    }
    return out, nil
}
```

Lifecycle for every `call()` invocation:

1. Plugin's `call()` implementation `malloc`s (C) or `Vec`-allocates (Rust) a buffer holding the
   JSON response bytes.
2. Plugin writes the pointer and length into `response->ptr` / `response->len` (the out-param) and
   returns.
3. Host copies those bytes into Go-managed memory (`C.GoBytes`) — after this point the plugin's
   buffer is no longer needed by the host.
4. Host calls `plugin.free_buffer(response.ptr, response.len)` — the **plugin's own** exported
   `free_buffer` function, not `free()` called directly by the host — because only the plugin's
   runtime/allocator knows how to correctly deallocate memory it allocated.

C reference plugin's `free_buffer` (plain `free()` because it allocated with `malloc`):

```c
static void plugin_free(void* ptr, size_t len) {
    (void)len;
    free(ptr);
}
```

C `write_response` helper that constructs the outgoing buffer:

```c
static void write_response(cliproxy_buffer* response, const char* text) {
    if (response == NULL || text == NULL) {
        return;
    }
    size_t len = strlen(text);
    void* ptr = malloc(len);
    if (ptr == NULL) {
        response->ptr = NULL;
        response->len = 0;
        return;
    }
    memcpy(ptr, text, len);
    response->ptr = ptr;
    response->len = len;
}
```

Note: allocates exactly `len` bytes, does **not** NUL-terminate.

Rust reference plugin's `free_buffer` must reconstruct a `Vec<u8>` from the raw parts so Rust's
global allocator reclaims it correctly, because it allocated with `Vec<u8>` +
`std::mem::forget`:

```rust
unsafe extern "C" fn plugin_free(ptr: *mut std::ffi::c_void, len: usize) {
    if !ptr.is_null() {
        let _ = Vec::from_raw_parts(ptr as *mut u8, len, len);  // reconstruct + drop => frees correctly
    }
}

fn write_response(response: *mut CliproxyBuffer, text: &str) {
    if response.is_null() {
        return;
    }
    let mut bytes = text.as_bytes().to_vec();
    let len = bytes.len();
    let ptr = bytes.as_mut_ptr();
    std::mem::forget(bytes);   // leak intentionally — ownership transfers to the C ABI boundary
    unsafe {
        (*response).ptr = ptr;
        (*response).len = len;
    }
}
```

`std::mem::forget(bytes)` is the Rust idiom for "hand this allocation across an FFI boundary
without running its destructor" — the buffer is intentionally leaked from Rust's perspective
until `plugin_free` reconstructs and drops it later. **Getting `Vec::from_raw_parts(ptr, len,
len)` wrong (e.g. mismatched capacity vs. length, or freeing via a different allocator) is
undefined behavior** — this is the single most dangerous spot for a Rust plugin author to get
subtly wrong, since `Vec::from_raw_parts` requires that `len == capacity` at the time of `forget`
(true here only because `to_vec()` produces an exact-capacity vec — if you ever
`Vec::with_capacity` and push less, you must call `.shrink_to_fit()` or track capacity
separately, or you'll corrupt the heap on free).

## Direction 2: plugin → host (host callbacks — the reverse path)

Symmetric but with allocator roles swapped: **when the plugin calls the host via
`host_api->call`, the HOST allocates the response buffer, and the plugin must call
`host_api->free_buffer` when done with it.**

Ground truth, `internal/pluginhost/host_callbacks_unix.go` (`cliproxyHostCall`, exported back to
C via `//export`):

```go
//export cliproxyHostCall
func cliproxyHostCall(hostCtx unsafe.Pointer, method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
    ...
    resp, errCall := entry.host.callFromPlugin(ctx, C.GoString(method), requestBytes)
    ...
    ptr := C.CBytes(resp)          // HOST allocates via cgo's C.CBytes (malloc'd C memory)
    if ptr == nil {
        return 1
    }
    response.ptr = ptr
    response.len = C.size_t(len(resp))
    return 0
}

//export cliproxyHostFree
func cliproxyHostFree(ptr unsafe.Pointer, len C.size_t) {
    if ptr != nil {
        C.free(ptr)    // host frees with plain C free(), matching C.CBytes's malloc
    }
}
```

These two Go functions (`cliproxyHostCall`, `cliproxyHostFree`) are exactly the C function
pointers wired into `cliproxy_host_api.call` / `cliproxy_host_api.free_buffer` at load time:

```c
static void cliproxy_set_host_api(cliproxy_host_api* api, uint32_t abi_version, void* host_ctx) {
    api->abi_version = abi_version;
    api->host_ctx = host_ctx;
    api->call = cliproxyHostCall;
    api->free_buffer = cliproxyHostFree;
}
```

Plugin-side usage pattern — call the host, then release its buffer immediately after use
(`examples/plugin/executor/c/src/plugin.c`):

```c
static void call_host(const char* method, const char* payload) {
    if (stored_host == NULL || stored_host->call == NULL || method == NULL) {
        return;
    }
    cliproxy_buffer response = {0};
    const uint8_t* request = (const uint8_t*)payload;
    size_t request_len = payload == NULL ? 0 : strlen(payload);
    if (stored_host->call(stored_host->host_ctx, method, request, request_len, &response) == 0
        && response.ptr != NULL && stored_host->free_buffer != NULL) {
        stored_host->free_buffer(response.ptr, response.len);   // must free the HOST's buffer via the HOST's free_buffer
    }
}
```

`host_ctx` is an **opaque token** (in practice a `uintptr_t` the host uses as a lookup key into a
`sync.Map` of live plugin instances — `hostCallbackEntries` in `loader_unix.go`). Plugin code
must pass it back unchanged on every host call; it must never dereference or interpret it. Note
this example also shows the required pattern for any plugin needing host callbacks: **persist the
`cliproxy_host_api*` handed to you in `cliproxy_plugin_init`** (it's only passed once) somewhere
reachable from your `call` implementation — the "simple" example ignores it
(`(void)host;`) because it makes no host callbacks, but any executor/HTTP/model/auth-file
plugin must stash it.

## Ownership summary table

| Direction | Who allocates | Who frees | Free mechanism |
|---|---|---|---|
| Plugin responds to host `call()` | Plugin | Host triggers, plugin executes | `plugin_api.free_buffer(ptr, len)` |
| Host responds to plugin's `host_api->call()` | Host (via cgo `C.CBytes`) | Plugin triggers, host executes | `host_api->free_buffer(ptr, len)` (→ `cliproxyHostFree` → C `free()`) |
| Request bytes passed *into* either `call()` | Caller (owns until call returns) | Caller frees after the call returns (host frees its `C.CBytes(request)` with a `defer C.free`) | N/A — request buffers are not returned across the boundary |

**Gotcha for plugin authors:** never call your own language's raw `free`/`drop` on a buffer that
came from `host_api->call`'s `response` out-param — it was allocated by the host's C allocator
via cgo, and only `host_api->free_buffer` knows how to correctly release it. Symmetrically, never
expect the host to `free()` your plugin's buffers directly — it always routes through your
`plugin_api.free_buffer`.

## Non-Go note (memory safety trade-off)

This ABI is language-neutral by design, but the manual ownership handoff above is the single
riskiest part of writing a plugin in a language without a GC:

- A buffer-ownership bug — freeing the wrong side's buffer, double-freeing, or a Rust
  `Vec::from_raw_parts` capacity mismatch — is **silent heap corruption, not a clean crash**.
- A crash, segfault, or `abort()` inside your plugin's `call()` takes down the entire host
  process — there is no process isolation (no WASM sandbox, no subprocess boundary).
- Panics in Rust code that unwind across the `extern "C"` boundary are undefined behavior unless
  caught — wrap `plugin_call`'s body in `std::panic::catch_unwind` and convert any panic into a
  well-formed `{"ok":false,"error":{...}}` envelope rather than let it unwind into C code.
- Go plugins sidestep almost all of this: the Go compiler generates the `cliproxy_plugin_init`
  export and cgo header for you, and `C.CBytes`/`C.GoBytes` handle the ownership handoff — the
  only "unsafe" surface is the handful of lines cgo auto-generates, not hand-written pointer
  arithmetic. Default to Go unless you have a specific reason (embedding an existing
  C/C++/Rust-only library, startup-latency/footprint constraints, or a Rust-first toolchain) to
  write a plugin in C or Rust — see `examples/plugin/simple/README.md`'s trust-boundary note,
  quoted in `abi-contract.md` §11.
- Prefer a real JSON library (`serde_json`, `cJSON`/`yyjson`) and a real base64 implementation
  over hand-rolled string scanning in production C/Rust plugins — the reference plugins avoid
  dependencies purely for demo purposes, not as a pattern to copy.
