<p align="center"><img src="./assets/logo.svg" width="72" height="72" alt="CLIProxyAPI Plugin Builder"></p>

# CLIProxyAPI Plugin Builder (`cpa`)

A Claude Code plugin that gives any session **full awareness of the CLIProxyAPI native plugin system** and a verified **scaffold → build → validate → wire → test** workflow for building plugins.

Pinned to upstream **[CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) v7.2.88**.

## Install
```
/plugin marketplace add pragmaticgrowth/cpa-plugins
/plugin install cpa@cpa-plugins
```

## What you get

**Skills** (auto-loaded when you work on CLIProxyAPI plugins):
| Skill | Covers |
|---|---|
| `cpa-plugin-overview` | What CLIProxyAPI is, the request lifecycle + plugin hook points, "which capability do I need?" |
| `cpa-plugin-abi` | The C ABI (`cliproxy_plugin_init`), JSON-RPC envelope, ~35 RPC methods, host callbacks, memory model |
| `cpa-capabilities` | The ~15 capabilities: purpose, interface, config, examples |
| `cpa-build-and-wire` | Verified build/install/enable/verify runbook + the plugin store |
| `cpa-go-plugin-authoring` | Canonical Go skeleton + real-world patterns |

**Commands:**
- `/cpa:scaffold <id> [--caps …]` — generate a buildable Go plugin project
- `/cpa:build` — `CGO_ENABLED=1 go build -buildmode=c-shared`
- `/cpa:validate` — confirm ABI exports, version, required metadata
- `/cpa:wire` — emit the `config.yaml` snippet + install path
- `/cpa:test` — build + install + verify via the Management API

**Subagent:** `cpa-plugin-author` — authors a complete plugin from a spec.

## Requirements for building plugins
- **Go 1.26+** and a C compiler (CGO). The CLIProxyAPI host must be built with cgo to load plugins.

## Ground truth & updates
`references/upstream/` vendors the real `pluginapi`/`pluginabi` types, three example plugins, and all capability docs at the pinned version. Refresh with:
```bash
plugins/cpa/scripts/refresh-upstream.sh [REF]
```
See `references/upstream/VERSION.txt` for the exact pin.
