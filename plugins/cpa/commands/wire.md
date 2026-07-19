---
description: Print the config.yaml snippet and install path to wire a plugin into a CLIProxyAPI host
argument-hint: <plugin-id> [--plugins-dir <path>] [--install]
---

Wire a built CLIProxyAPI plugin into a host. Arguments: `$ARGUMENTS`.

1. Determine `<plugin-id>`, the host `plugins.dir` (default `plugins`, ask if unknown), `GOOS`/`GOARCH` (`go env GOOS GOARCH`), and the extension (`dylib`/`so`/`dll`).
2. Print the exact **install path**:
   ```
   <plugins-dir>/<GOOS>/<GOARCH>/<plugin-id>.<ext>
   ```
   (The host also accepts a flat `<plugins-dir>/<plugin-id>.<ext>`, but the platform path is preferred.)
3. Print the **config.yaml** block to merge (fill in the plugin's own ConfigFields):
   ```yaml
   plugins:
     enabled: true
     dir: "<plugins-dir>"
     configs:
       <plugin-id>:
         enabled: true
         priority: 1
   ```
4. If `--install` was passed, copy the built artifact to the install path (`mkdir -p` first).
5. Remind: `plugins.enabled: true` is the global master switch; per-plugin `enabled` does not flip it. Changes hot-reload via the config watcher, or restart the host.

Use the **cpa-build-and-wire** skill for discovery/priority details. Then suggest `/cpa:test`.
