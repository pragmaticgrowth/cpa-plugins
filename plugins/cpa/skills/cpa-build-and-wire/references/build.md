# Build: toolchain, commands, cross-compile caveat

Pinned to upstream v7.2.88. Verified live on macOS arm64 (Go 1.26.5, Apple clang 21):
the `simple` Go example compiles to a valid Mach-O arm64 `c-shared` dylib exporting
`cliproxy_plugin_init` (the symbol the host `dlsym`s for).

## Prerequisites

| Tool | Version | Why |
|---|---|---|
| Go | 1.26.x (`go.mod`: `go 1.26.0`) | builds the plugin; `-buildmode=c-shared` |
| CGO + C compiler | Apple clang (Xcode) / gcc | `c-shared` requires cgo; set `CGO_ENABLED=1` |
| cmake | ≥3.x | only for **C** plugins |
| cargo/rustc | stable | only for **Rust** plugins |
| CLIProxyAPI host binary | built **with cgo** | plugin loading is a hard error without cgo, see `loader_unsupported.go` |

macOS note: `CGO_ENABLED=1` is the default on macOS when a C compiler is present, but set
it explicitly in build scripts so CI/other platforms behave the same way.

## The core build command (Go)

From inside the plugin's Go module directory:

```bash
cd <plugin>/go
CGO_ENABLED=1 go build -buildmode=c-shared -o <id>.<ext> .
```

- Extension by OS: **macOS → `dylib`**, **Linux/FreeBSD → `so`**, **Windows → `dll`**.
- `-buildmode=c-shared` also emits a `<id>.h` header next to the artifact — not needed at
  runtime; the example build harness deletes it (`rm -f <id>.h`).
- `go.mod` for a real (non-vendored) plugin needs the **published** SDK module:
  ```
  require github.com/router-for-me/CLIProxyAPI/v7 vX.Y.Z
  ```
  The upstream examples instead use a dev-only local resolve:
  ```
  module github.com/router-for-me/CLIProxyAPI/v7/examples/plugin/simple/go
  go 1.26.0
  require github.com/router-for-me/CLIProxyAPI/v7 v7.0.0
  replace github.com/router-for-me/CLIProxyAPI/v7 => ../../../..
  ```
  Do not ship a `replace` directive pointing at a local checkout in a real plugin's `go.mod`.

## Manual per-language build commands (from the examples tree)

macOS, Go:

```bash
mkdir -p plugins/darwin/$(go env GOARCH)
go build -buildmode=c-shared -o plugins/darwin/$(go env GOARCH)/simple-go.dylib ./examples/plugin/simple/go
rm -f plugins/darwin/$(go env GOARCH)/simple-go.h
```

macOS, C (CMake):

```bash
mkdir -p plugins/darwin/$(go env GOARCH)
cmake -S examples/plugin/simple/c -B /tmp/cliproxy-simple-c-build \
  -DCMAKE_LIBRARY_OUTPUT_DIRECTORY=$PWD/plugins/darwin/$(go env GOARCH)
cmake --build /tmp/cliproxy-simple-c-build
```

macOS, Rust (`cdylib` crate):

```bash
mkdir -p plugins/darwin/$(go env GOARCH)
cd examples/plugin/simple/rust
CARGO_TARGET_DIR=/tmp/cliproxy-simple-rust-target cargo build --release --locked
cp /tmp/cliproxy-simple-rust-target/release/libcliproxy_simple_rust.dylib \
  ../../../../plugins/darwin/$(go env GOARCH)/simple-rust.dylib
```

For Linux, FreeBSD, or Windows: same source directories, platform extension.

## The examples Makefile (reference build harness)

`examples/plugin/Makefile` builds every `<example>-<lang>.<ext>` into `examples/plugin/bin/`:

- Go rule: `cd $*/go && go build -buildmode=c-shared -o bin/$*-go.$(EXT) .`
- C rule: `cmake -S $*/c -B build/$*/c -DCMAKE_LIBRARY_OUTPUT_DIRECTORY=bin && cmake --build ...`
- Rust rule: `cargo build --release --locked` then copy `libcliproxy_<name>_rust.<ext>` → `bin/`.

```bash
make -C examples/plugin list     # list buildable examples
make -C examples/plugin build    # build all of them
make -C examples/plugin clean
```

The Makefile output (`bin/<name>-<lang>.<ext>`) is a **build** convention only, not the
host discovery layout — the plugin ID is the dynamic library basename without the platform
extension, so a Makefile-built `simple-go.dylib` maps to `plugins.configs.simple-go`. You
must place/rename the artifact per the discovery rules — see `discovery-and-install.md` —
before the host will find it.

## Cross-compilation caveat

`GOOS`/`GOARCH` cross-compiles pure Go, but `-buildmode=c-shared` requires cgo, so
cross-building a plugin needs a **cross C toolchain** for the target
(`CC=<cross-clang-or-gcc>`, and typically `CXX` too). In practice, build each target
platform natively (or in a matching Docker/CI runner for that `GOOS/GOARCH`) rather than
attempting to cross-compile cgo from a single machine. Ship one artifact per
`GOOS/GOARCH` combination you intend to support, named/placed per
`discovery-and-install.md`.

## Why the host binary itself needs cgo

Plugin *loading* is a host-side capability, independent of the plugin's own build.
`internal/pluginhost` has three build-tag-selected loader implementations:

- `loader_unix.go` (`//go:build cgo && (linux || darwin || freebsd)`) — the real dlopen loader.
- `loader_windows.go` (`//go:build windows`) — `LoadDLL` with a shadow-copy trick (no cgo needed on Windows).
- `loader_unsupported.go` (`//go:build !cgo && !windows`) — a stub that always errors
  `"standard dynamic library plugin loading requires cgo on this platform"`.

So a `CGO_ENABLED=0` CLIProxyAPI binary cannot load *any* native plugin on
Linux/macOS/FreeBSD, regardless of how the plugin itself was built. Confirm host build
capability via the `X-CPA-SUPPORT-PLUGIN` response header on management API responses
(`1` = supports plugins, `0` = does not) — see `verify.md`.
