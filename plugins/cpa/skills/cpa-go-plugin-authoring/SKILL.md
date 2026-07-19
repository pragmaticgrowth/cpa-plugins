---
name: cpa-go-plugin-authoring
description: Use when writing or editing the Go source of a CLIProxyAPI native plugin — the cgo c-shared skeleton, the four required exports, handleMethod dispatch, plugin.register/reconfigure, config parsing, host callbacks (host.http/host.model/host.auth), streaming executors, or model-router plugins for CLIProxyAPI v7.2.88.
---

# Writing a CLIProxyAPI Go plugin

A CLIProxyAPI plugin is a Go `-buildmode=c-shared` library (`.dylib`/`.so`/`.dll`) loaded
in-process by the host via `dlopen`. It is **not** a Go `plugin.Open()` plugin. Everything
crossing the boundary is `(pointer, length)` raw JSON, wrapped in a `{ok,result,error}`
envelope, dispatched by a single multiplexed `call()` function keyed on a method-name string.
This is fully trusted, unsandboxed, in-process code — a plugin panic/crash takes the host down.

Pinned to upstream **v7.2.88**. Ground truth: `${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginabi-types.go`,
`${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginapi-types.go`.

## Fastest path: scaffold, don't hand-roll

Run **`/cpa:scaffold`** to generate a new plugin from
`${CLAUDE_PLUGIN_ROOT}/templates/go-plugin/` (`main.go.tmpl`, `go.mod.tmpl`, `Makefile`,
`config.snippet.yaml`). The template already wires the four exports, the envelope helpers, and
stub cases for request-normalizer/translator; you flip capability flags and fill in logic. Only
hand-write the skeleton below if you're editing an existing plugin or need to understand what
the scaffold generated.

## The canonical skeleton

Every plugin's `main.go` has four parts, in this order:

**1. cgo preamble** — declares the fixed C ABI structs (`cliproxy_buffer`,
`cliproxy_host_api`, `cliproxy_plugin_api`) and forward-declares the three Go-exported
functions the preamble references. This block is copied verbatim, never edited:

```go
package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct { void* ptr; size_t len; } cliproxy_buffer;

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

If your plugin calls back into the host (`host.http.*`, `host.model.*`, `host.auth.*`), also add
a `store_host_api`/`call_host_api`/`free_host_buffer` C helper block — see
`references/real-world-patterns.md`.

**2. Four exported symbols** — fixed names, never renamed, resolved by the host after `dlopen`:
`cliproxy_plugin_init` (bootstrap: fills the `plugin` out-param's vtable, returns `0`/non-zero),
`cliproxyPluginCall` (the RPC dispatcher wired into `plugin.call`), `cliproxyPluginFree` (frees a
response buffer this plugin allocated, `C.free` matching `writeResponse`'s `C.CBytes`),
`cliproxyPluginShutdown` (teardown hook, usually empty). Plus a no-op `func main() {}` required
by `-buildmode=c-shared`.

**3. `handleMethod` dispatch** — a `switch method` over `pluginabi.MethodXxx` string constants
(from `sdk/pluginabi`), each case unmarshaling a `pluginapi` request type and returning a
marshaled `pluginapi` response type wrapped in `okEnvelope`/`errorEnvelope`. `unknown_method` is
a normal `ok:false` envelope, **not** a Go `error` — reserve `error` for cases where you can't
even build a response.

**4. `plugin.register` / `plugin.reconfigure`** — share one handler, returning:

```go
type registration struct {
	SchemaVersion uint32                 `json:"schema_version"`
	Metadata      pluginapi.Metadata     `json:"metadata"`
	Capabilities  registrationCapability `json:"capabilities"`
}
```

`pluginapi.Metadata{Name, Version, Author, GitHubRepository, Logo, ConfigFields}` — `Name` must
match the plugin's config key and library filename. `Capabilities` is a **local, snake_case-tagged
bool/string/slice mirror** of the host's interface-typed `pluginapi.Capabilities` — only the
flags you turn `true` matter; the rest default false/zero. `SchemaVersion` is always
`pluginabi.SchemaVersion` (currently `1`) — new capabilities are gated by flags/method names, not
schema bumps.

Full annotated skeleton with every line explained:
`${CLAUDE_PLUGIN_ROOT}/skills/cpa-go-plugin-authoring/references/skeleton.md`

## Non-negotiable rules

- **Never rename the four exports** — the host resolves them by exact symbol name.
- **No Go types cross the ABI** — no interfaces, slices, maps, channels, `context.Context`,
  `error`. Marshal/unmarshal JSON on both sides.
- **`[]byte` fields serialize as base64** (`AuthData.StorageJSON`, `ExecutorResponse.Payload`, …).
- **You allocate, you free, with C's allocator** — `writeResponse` uses `C.CBytes`
  (`malloc`); `cliproxyPluginFree` uses `C.free`. Never let Go's GC own this memory.
- **Executor requires both `executor_input_formats` and `executor_output_formats`** declared, or
  the host can't translate around it. Recognized formats: `chat-completions`, `responses`,
  `claude` (+ legacy aliases `openai`, `openai-response`).
- **Package-level state must be concurrency-safe** — the shared library is one long-lived
  process, called from multiple goroutines. Single scalar → `atomic.Bool`/`atomic.Int64`;
  multi-field config → `atomic.Value` holding an immutable struct, replaced wholesale.
- **Fail open on transforms** — a normalizer/translator that isn't sure what to do returns the
  *unmodified* body, never an error.
- **`Handled: false` = polite decline; `errorEnvelope(...)` = active rejection.** Every
  routing/scheduling capability (`model.route`, `scheduler.pick`, `frontend_auth.authenticate`)
  uses a `Handled`/`Authenticated` bool for "not mine, let something else try."

## Key real-world patterns (see the reference for full code)

- **Config**: `plugin.register`/`reconfigure` request body is `{"config_yaml": <bytes>}` — you
  own the YAML schema end-to-end via `yaml.v3` + `yaml:"..."` tags; `ConfigFields` metadata is
  descriptive only (drives management UI), never enforced by the host.
- **Host callbacks**: a `callHost(method, payload)` helper marshals→`C.CBytes`→
  `C.call_host_api`→unmarshal-envelope, used for `host.http.do`, `host.model.execute[_stream]`,
  `host.auth.*`. Forward `HostCallbackID` on nested `host.model.*` calls so the host skips your
  own interceptor chain on the nested call.
- **Streaming**: `executor.execute_stream` returns *immediately* with headers only; work happens
  in a `recover()`-guarded goroutine that `host.stream.emit`s chunks and finishes with
  `host.stream.close`. This is a different stream-ID flow from *consuming* a nested
  `host.model.execute_stream` (host hands back its own `StreamID`; read-loop via
  `host.model.stream_read` until `Done`, then `host.model.stream_close`).
- **Model routing**: `model.route` returns `pluginapi.ModelRouteResponse{Handled, TargetKind,
  Target, TargetModel, Reason}` — `TargetKind: ModelRouteTargetProvider` hands off to a built-in
  provider by key; `TargetKind: ModelRouteTargetSelf` hands the request to the router plugin's
  *own* executor (needed for multi-backend fallback/retry logic a plain provider route can't
  express).

Full walkthroughs: `${CLAUDE_PLUGIN_ROOT}/skills/cpa-go-plugin-authoring/references/real-world-patterns.md`

## Vendored ground truth (read these, don't guess)

- `${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/simple/go/main.go` — the full
  mixed-capability reference (every method, SDK-typed style).
- `${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/codex-service-tier/go/main.go` — smallest
  real single-capability plugin (`request_normalizer` + config parsing + `sjson` surgical edit).
- `${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/claude-web-search-router/go/` — the only
  multi-file example: `model_router` + `executor` combined, host-model-callback streaming,
  penalty-based backend fallback.
- `${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginapi-types.go` /
  `${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginabi-types.go` — the actual struct/constant
  definitions; treat as the authoritative field list over any example or doc.
