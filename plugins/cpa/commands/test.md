---
description: Build, install, and verify a plugin is registered against a running CLIProxyAPI host
argument-hint: <plugin-id> [--url http://127.0.0.1:PORT] [--plugins-dir <path>]
---

End-to-end test of a CLIProxyAPI plugin. Arguments: `$ARGUMENTS`.

1. **Build + validate** the plugin (reuse the `/cpa:build` and `/cpa:validate` steps).
2. **Install** it to `<plugins-dir>/<GOOS>/<GOARCH>/<plugin-id>.<ext>` (see `/cpa:wire`), and ensure `config.yaml` has `plugins.enabled: true` + `plugins.configs.<plugin-id>.enabled: true`.
3. **Reload** the host (config watcher hot-reloads, or restart).
4. **Verify against the Management API** (requires the host running + a management key in `$CPA_MGMT_KEY`, base URL via `--url` or `$CPA_BASE_URL`):
   ```bash
   curl -s -H "Authorization: Bearer $CPA_MGMT_KEY" "$CPA_BASE_URL/v0/management/plugins" \
     | jq '.[] | select(.id=="<plugin-id>") | {id, registered, effective_enabled, error}'
   ```
   Expect `registered: true`, `effective_enabled: true`, no `error`.
5. If no host/key is available, **degrade gracefully**: run build+validate only and clearly report that the running-instance check was skipped (and how to run it).

If registration fails, check for a missing required `Metadata` field, an ABI-version mismatch, or a filename/config-key mismatch — use the **cpa-build-and-wire** skill's failure guide.
