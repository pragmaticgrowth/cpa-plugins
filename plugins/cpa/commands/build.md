---
description: Build a CLIProxyAPI native Go plugin to a c-shared shared library
argument-hint: [path-to-plugin-dir]
---

Build the CLIProxyAPI plugin in `$ARGUMENTS` (default: current directory).

1. Confirm the toolchain: `go version` (need **1.26+**) and `CGO_ENABLED=1` (a C compiler must be present).
2. Determine the plugin id (the `Metadata.Name` in `main.go`, which should match the module/dir) and the host extension: `dylib` (macOS), `so` (Linux), `dll` (Windows).
3. Build:
   ```bash
   cd <dir>
   go mod tidy
   CGO_ENABLED=1 go build -buildmode=c-shared -o <id>.<ext> .
   rm -f <id>.h    # generated header, not needed at runtime
   ```
   If a `Makefile` is present, `make build` does the same.
4. Report the artifact path, size, and `file <artifact>` output. If the build fails, use the **cpa-build-and-wire** skill (and **systematic-debugging**) — do not guess.

Then suggest `/cpa:validate` to confirm the ABI exports.
