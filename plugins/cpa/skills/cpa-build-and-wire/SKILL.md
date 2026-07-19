---
name: cpa-build-and-wire
description: Use when building, installing, wiring config.yaml, or verifying a CLIProxyAPI native plugin (Go/C/Rust c-shared dylib/so/dll) — covers the CGO build command, plugins/<GOOS>/<GOARCH> discovery layout, plugins.* config.yaml schema, and the GET /v0/management/plugins verification checklist.
---

# CLIProxyAPI plugin: build, install, wire, verify

CLIProxyAPI plugins are native dynamic libraries (`.so`/`.dylib`/`.dll`), loaded with
`dlopen`/`LoadDLL` — not Go's `plugin.Open`. The host talks to them over a single C ABI
call (`cliproxy_plugin_init`) and a JSON-RPC-over-bytes contract on top of it. This skill
covers everything after the plugin's Go/C/Rust code is written: turning source into an
artifact, placing it where the host finds it, wiring `config.yaml`, and proving it loaded.
Pinned to upstream **v7.2.88**.

## Toolchain

| Tool | Requirement | Why |
|---|---|---|
| Go | 1.26.x (`go.mod` says `go 1.26.0`) | builds the plugin; `-buildmode=c-shared` |
| CGO + a C compiler | `CGO_ENABLED=1`, clang/gcc | `c-shared` requires cgo |
| cmake | only for **C** plugins | example `c` build uses CMake |
| cargo/rustc | only for **Rust** plugins | example `rust` build is a `cdylib` crate |
| CLIProxyAPI host binary | built **with cgo** | on Linux/macOS/FreeBSD, a `CGO_ENABLED=0` host binary cannot load *any* native plugin (`loader_unsupported.go` stub errors outright); check the `X-CPA-SUPPORT-PLUGIN` response header (`1`/`0`) to confirm |

## Build (the core command)

From the plugin's Go module directory:

```bash
CGO_ENABLED=1 go build -buildmode=c-shared -o <id>.<ext> .
```

Extension by OS: **macOS → `dylib`**, **Linux/FreeBSD → `so`**, **Windows → `dll`**.
`-buildmode=c-shared` also emits `<id>.h` next to the artifact — delete it, the host
never reads it. A standalone (non-vendored) plugin module requires the *published* SDK:
`require github.com/router-for-me/CLIProxyAPI/v7 vX.Y.Z` (the example modules instead use
a local `replace ... => ../../../..` for dev only).

**Cross-compilation caveat:** `GOOS`/`GOARCH` cross-compiles Go, but `-buildmode=c-shared`
needs cgo, so cross-building needs a matching cross C toolchain (`CC=<cross-clang>`). In
practice build each target on its native platform or a matching CI runner — ship one
artifact per `GOOS/GOARCH` you support.

See `${CLAUDE_PLUGIN_ROOT}/skills/cpa-build-and-wire/references/build.md` for the
examples-tree Makefile convention and per-language manual build commands.

## Artifact placement (host discovery)

The host scans `plugins.dir` (default `plugins`, `~` expands to home) in this order:

1. `<plugins.dir>/<GOOS>/<GOARCH>/` — **preferred**, and what the plugin store always writes to.
2. `<plugins.dir>/` — flat fallback, for hand-placed single-platform deploys.

Plugin **ID = filename without extension**: `<id>.<ext>`, or a versioned
`<id>-v<version>.<ext>` (e.g. `my-plugin-v1.2.0.dylib`) — newest semver wins per ID unless
`plugins.configs.<id>.store.version` pins an exact version. ID must match
`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`.

```
<plugins.dir>/darwin/arm64/my-plugin.dylib
```

See `${CLAUDE_PLUGIN_ROOT}/skills/cpa-build-and-wire/references/discovery-and-install.md`
for the full selection algorithm, hot-swap/fuse behavior, and a documented-vs-source
discrepancy worth knowing about.

## config.yaml wiring

```yaml
plugins:
  enabled: true              # GLOBAL master switch — MUST be true or nothing loads
  dir: "plugins"
  configs:
    my-plugin:                # key MUST equal the plugin ID (filename sans ext)
      enabled: true            # per-plugin switch — does NOT flip the global switch
      priority: 1               # higher wins on route/model/flag conflicts (ties: alpha by id)
      # ...arbitrary custom fields your plugin declares via Metadata.ConfigFields...
```

- Per-instance `enabled` defaults to **false** — plugins are opt-in even once the file is on disk.
- The **entire** `plugins.configs.<id>` YAML subtree is preserved losslessly and handed to
  the plugin verbatim as `ConfigYAML` bytes on `plugin.register`/`plugin.reconfigure` — the
  host only ever interprets `enabled`/`priority` itself.
- Optional `plugins.store-sources` (extra registry URLs) and `plugins.store-auth`
  (token-env-based auth for private registries) exist for **distributing** plugins via the
  plugin **store** (`registry.json` — separate from the host discovery mechanism above;
  the host just discovers whatever ends up on disk).

Full schema, `store:` version pinning, and store-auth types are in
`${CLAUDE_PLUGIN_ROOT}/skills/cpa-build-and-wire/references/config-wiring.md`.

## Runbook: build → install → enable → verify

```bash
# 1. Build
cd path/to/my-plugin/go
CGO_ENABLED=1 go build -buildmode=c-shared -o my-plugin.dylib .

# 2. Install into the host's plugins dir (platform layout)
mkdir -p "$CPA_PLUGINS_DIR/darwin/arm64"
cp my-plugin.dylib "$CPA_PLUGINS_DIR/darwin/arm64/my-plugin.dylib"

# 3. Enable in config.yaml: plugins.enabled: true, plugins.configs.my-plugin.enabled: true

# 4. Reload — the config-file watcher hot-reloads on save (ApplyConfig), or restart the host.

# 5. Verify
curl -s -H "Authorization: Bearer $MGMT_KEY" \
  http://127.0.0.1:<mgmt-port>/v0/management/plugins | jq
```

## Verify

Look for your plugin's entry with `"registered": true` and `"effective_enabled": true`.
Common failure modes: metadata missing `Name`/`Version`/`Author`/`GitHubRepository` →
registration silently discarded (check host logs, not the API); ABI mismatch
(`abi_version != 1`) → load rejected outright; a crashed ("fused") plugin only recovers by
changing which file is selected for it (version bump) or a host restart.

Full endpoint table (`GET/PATCH/PUT /v0/management/plugins/...`, plugin-store endpoints)
and failure-mode diagnosis in
`${CLAUDE_PLUGIN_ROOT}/skills/cpa-build-and-wire/references/verify.md`.

## Plugin store (distribution, not development)

A separate `registry.json`-based store subsystem (`internal/pluginstore`) exists for
installing/updating plugins others published (GitHub-release or direct-artifact zips,
SHA-256 verified). It writes to the exact same `<plugins.dir>/<GOOS>/<GOARCH>/` layout
described above — it is how artifacts *arrive*, not a different loading mechanism. Not
needed to build and hand-install your own plugin; see
`${CLAUDE_PLUGIN_ROOT}/skills/cpa-build-and-wire/references/config-wiring.md` if you're
publishing one.
