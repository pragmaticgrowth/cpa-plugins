---
name: cpa-plugin-overview
description: Use when you're about to build, extend, or debug a CLIProxyAPI native plugin and need the big picture first — what CLIProxyAPI is, the SDK-embedding vs native-plugin-system distinction, the end-to-end request lifecycle (frontend auth, model router, interceptors, scheduler, executor, translators, thinking, usage) and which of its ~18 plugin capabilities to reach for. Start here before cpa-plugin-abi, cpa-capabilities, cpa-go-plugin-authoring, or cpa-build-and-wire.
---

# CLIProxyAPI Plugin System — Overview

CLIProxyAPI is a Go proxy server (`github.com/router-for-me/CLIProxyAPI/v7`, pinned here to
upstream **v7.2.88**) that exposes OpenAI-, Claude-, Gemini-, and Grok-compatible HTTP surfaces
(`/v1/chat/completions`, `/v1/messages`, `/v1beta/models/*action`, `/v1/responses`, ...) and routes
them to upstream **CLI-subscription-based provider accounts** via OAuth-derived credentials, plus
plain API keys and OpenAI-compatible upstreams. Fronted providers: **Gemini** (incl. Vertex /
AI Studio), **Claude** (Claude Code OAuth), **Codex/OpenAI** (incl. a WebSocket executor),
**Antigravity**, **xAI/Grok**, **Kimi** (Moonshot K2), and generic **OpenAI-compatible** upstreams
(e.g. OpenRouter).

## Two extension mechanisms — do not conflate them

1. **Go SDK embedding** (`sdk/cliproxy`, `docs/sdk-usage.md`, `docs/sdk-advanced.md`) — a Go
   program imports the SDK, builds a `Service`, and registers a custom `auth.ProviderExecutor`
   in-process via `coreauth.Manager.RegisterExecutor(...)` plus custom translators via
   `sdktranslator.Register(...)`. No RPC boundary, no dynamic loading — you compile it in. (Note:
   `docs/sdk-usage.md`/`docs/sdk-advanced.md` show `/v6` import paths — outdated; the module is
   `/v7`.)
2. **The native plugin system** (`sdk/pluginapi`, `sdk/pluginabi`, `internal/pluginhost`) — a
   plugin is a **native shared library** (`.so`/`.dylib`/`.dll`) loaded via `dlopen`/`dlsym`,
   exposing a small C ABI (`cliproxy_plugin_init`) that carries a synchronous JSON-RPC-like
   envelope (`pluginabi.Envelope{OK, Result, Error}`) over method names like `scheduler.pick`,
   `model.route`, `request.translate`. This is the system with the many named "hooks" a plugin
   declares via `pluginapi.Capabilities` — **this skill family is about building that.** ABI wire
   details: `${CLAUDE_PLUGIN_ROOT}/skills/cpa-plugin-abi/SKILL.md`.

## Request lifecycle — where each hook fires

Every inbound request, regardless of entry protocol, flows through
`sdk/api/handlers/handlers.go`'s `BaseAPIHandler` and `sdk/cliproxy/auth/conductor.go`'s
`Manager.Execute`, in this order:

| # | Stage | Hook capability | Host call site |
|---|---|---|---|
| 0 | Inbound HTTP auth (before proxy routing) | `FrontendAuthProvider` (+ `FrontendAuthProviderExclusive`) | `internal/access` bridge |
| 1 | Model routing, before provider/model resolution | `ModelRouter` | `applyModelRouter` → `host.RouteModel` |
| 2 | Request, pre credential-selection | `RequestInterceptor.InterceptRequestBeforeAuth` | `applyRequestInterceptorsBeforeAuth` |
| 3 | Credential/auth selection ("scheduler") | `Scheduler.Pick` | `pickViaPluginScheduler`, falls back to built-in round-robin/fill-first |
| 4 | Request, post credential-selection, pre translation | `RequestInterceptor.InterceptRequestAfterAuth` | `applyRequestAfterAuthInterceptor` (conductor.go) |
| 5 | Request format translation (entry → provider) | `RequestNormalizer` (always-on) then `RequestTranslator` (fallback, only if no built-in `from→to` transform) | `sdktranslator` Pipeline/Registry, process-global |
| 6 | Reasoning/thinking config → provider payload | `ThinkingApplier` (plugin-owned provider keys only) | `internal/thinking.ApplyThinking` |
| 7 | Upstream call | `Executor` (`ProviderExecutor`) — built-in or plugin-owned, can replace the whole provider | `executor.Execute`/`ExecuteStream` |
| 8 | Response format translation (provider → response) | `ResponseBeforeTranslator` → native/registered translation → `ResponseTranslator` (fallback) → `ResponseAfterTranslator` (always-on) | `sdktranslator`, same global registry |
| 9 | Final response before delivery | `ResponseInterceptor` (non-streaming) / `StreamChunkInterceptor` (per SSE chunk, bounded history, `DropChunk`) | `applyResponseInterceptors` / `interceptStreamChunk` |
| 10 | Post-completion | `UsagePlugin.HandleUsage` | `sdk/cliproxy/usage/manager.go` |
| — | Startup / out-of-band | `ModelRegistrar`, `ModelProvider`, `AuthProvider`, `CommandLinePlugin`, `ManagementAPI` | plugin-load time / on-demand |

Two things that trip up new plugin authors:
- Steps 5 and 8 (`RequestNormalizer`/`RequestTranslator`/`ResponseTranslator`/`ResponseBeforeTranslator`/`ResponseAfterTranslator`)
  are **not** executor-scoped — they're wired into the single process-global `sdktranslator`
  registry (`sdktranslator.SetPluginHooks(pluginHost)`), so they run for *every* translation in
  the process, including ones performed by built-in executors, not just plugin executors.
- A panicking plugin hook is caught by `recover()` and **fuses** that plugin (skipped for the rest
  of that snapshot's lifetime) rather than crashing the host — but only for that one plugin.

Full prose + a mermaid diagram of every stage and hook:
`${CLAUDE_PLUGIN_ROOT}/skills/cpa-plugin-overview/references/lifecycle.md`

## Which capability do I need?

| Developer intent | Capability |
|---|---|
| Add a whole new upstream/backend that speaks its own HTTP API | `Executor` (`ProviderExecutor`) |
| Route specific requests to a different backend *before* auth/provider resolution | `ModelRouter` |
| Register a fixed, version-pinned model list that never varies per account | `ModelRegistrar` |
| Register models that differ per logged-in account or need a live upstream list call | `ModelProvider` |
| Add OAuth/device-code/API-key-file credential lifecycle for a new upstream | `AuthProvider` |
| Add a custom scheme for authenticating *inbound* clients to CLIProxyAPI | `FrontendAuthProvider` (+ `Exclusive`) |
| Override which credential/auth record services a request | `Scheduler` |
| Convert between two genuinely different wire protocols | `RequestTranslator` / `ResponseTranslator` |
| Patch/fill a request that's already in the right protocol shape | `RequestNormalizer` |
| Fix a raw provider response before native translation runs | `ResponseBeforeTranslator` |
| Final compat shim on an already-translated client response | `ResponseAfterTranslator` |
| Inject/rewrite headers before or after credential selection | `RequestInterceptor` |
| Rewrite headers/body of a completed non-streaming response | `ResponseInterceptor` |
| Rewrite or drop individual SSE stream chunks | `StreamChunkInterceptor` |
| Write canonical thinking config into your own provider's payload shape | `ThinkingApplier` |
| Observe token usage/latency for external billing/stats, read-only | `UsagePlugin` |
| Add custom CLI flags / one-shot diagnostics to the binary | `CommandLinePlugin` |
| Add an admin UI panel or authenticated API route for your plugin | `ManagementAPI` |

One-line "why" for each, plus doc/type pointers:
`${CLAUDE_PLUGIN_ROOT}/skills/cpa-plugin-overview/references/capability-decision-guide.md`

## Ground truth

- `${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginapi-types.go` — the `Capabilities` struct and every request/response type.
- `${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginabi-types.go` — RPC method-name constants, `Envelope`.
- `${CLAUDE_PLUGIN_ROOT}/references/upstream/docs-plugin/development.md` — full authoring workflow.
- `${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/` — one buildable example per capability.

## Next skills

Once you know which capability you need: ABI wire contract →
`${CLAUDE_PLUGIN_ROOT}/skills/cpa-plugin-abi/SKILL.md`; capability-specific request/response
shapes → `cpa-capabilities`; writing the Go plugin → `cpa-go-plugin-authoring`; building/wiring
the `.so`/`.dylib` into a running host → `cpa-build-and-wire`.
