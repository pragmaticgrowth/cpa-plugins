<p align="center"><img src="./public/logo.svg" width="88" height="88" alt="cpa-plugins"></p>

<h1 align="center">cpa-plugins</h1>

<p align="center">A Claude Code plugin marketplace for building <a href="https://github.com/router-for-me/CLIProxyAPI">CLIProxyAPI</a> native plugins.</p>

---

## Install

```
/plugin marketplace add pragmaticgrowth/cpa-plugins
/plugin install cpa@cpa-plugins
```

## Plugins in this marketplace

### `cpa` — CLIProxyAPI Plugin Builder
Turns any Claude Code session into an expert CLIProxyAPI native-plugin builder. It bundles:

- **5 skills** — the C ABI, the ~15 capabilities, the request lifecycle + hook points, the verified build/wire runbook, and the canonical Go authoring patterns.
- **5 commands** — `/cpa:scaffold`, `/cpa:build`, `/cpa:validate`, `/cpa:wire`, `/cpa:test`.
- **1 subagent** — `cpa-plugin-author`, which authors a complete plugin from a spec.
- **Vendored ground-truth** — the real `pluginapi`/`pluginabi` types, three example plugins, and all capability docs, pinned to upstream **v7.2.88** and refreshable.

See [`plugins/cpa/README.md`](./plugins/cpa/README.md).

## What is a CLIProxyAPI plugin?
CLIProxyAPI is a proxy that exposes OpenAI/Claude/Gemini/Codex/Grok-compatible APIs over your CLI accounts. A **native plugin** is a shared library (`.dylib`/`.so`/`.dll`, built from Go/C/Rust) loaded in-process, exporting one C symbol (`cliproxy_plugin_init`) and speaking JSON-RPC to the host. Plugins can add providers, auth, executors, request/response translators & normalizers, interceptors, schedulers, model routers, thinking-appliers, usage observers, CLI flags, and management-API routes.

## Requirements (to build plugins)
- **Go 1.26+** and a C compiler (CGO). The CLIProxyAPI host must be built with cgo to load plugins.

## Repo layout
```
.claude-plugin/marketplace.json   # marketplace catalog
plugins/cpa/                       # the plugin (skills, commands, agents, templates, references)
docs/superpowers/specs/            # design spec
```

## Development
See [CLAUDE.md](./CLAUDE.md) for how the plugin is structured, how to add a skill, how to refresh the vendored upstream, and how to dogfood locally.

## License
MIT (this repo). Vendored upstream in `plugins/cpa/references/upstream/` remains under CLIProxyAPI's MIT license — see its `NOTICE.md`.
