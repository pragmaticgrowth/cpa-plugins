---
description: Validate a built CLIProxyAPI plugin — ABI exports, version, and required metadata
argument-hint: [path-to-artifact-or-dir]
---

Validate a CLIProxyAPI plugin artifact (`$ARGUMENTS`, default: the `.dylib`/`.so`/`.dll` in the current dir).

Check, and report a pass/fail for each:

1. **ABI entrypoint exported.** The shared library must export `cliproxy_plugin_init` (plus `cliproxyPluginCall`, `cliproxyPluginFree`, `cliproxyPluginShutdown`):
   ```bash
   nm -gU <artifact> | grep cliproxy   # macOS/Linux; on Linux use: nm -D <artifact> | grep cliproxy
   ```
2. **ABI version.** Source must set `plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)` (== 1). A mismatch is rejected at load.
3. **Required metadata present.** `Metadata.Name`, `Version`, `Author`, and `GitHubRepository` must all be non-empty in `exampleRegistration()` — a missing field makes the host silently discard the registration.
4. **Executor sanity (if advertised).** If `Executor: true`, `ExecutorInputFormats` and `ExecutorOutputFormats` must be non-empty.
5. **Config key match.** `Metadata.Name` should equal the intended `plugins.configs.<id>` key and the installed filename `<id>.<ext>`.

Use the **cpa-build-and-wire** and **cpa-plugin-abi** skills for the exact rules. If anything fails, explain the fix. On success, suggest `/cpa:wire`.
