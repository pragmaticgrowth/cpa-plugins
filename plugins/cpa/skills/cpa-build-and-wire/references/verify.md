# Verify: Management API checks & common load failures

Pinned to upstream v7.2.88. Source: `internal/pluginhost/host.go`,
`internal/pluginhost/management.go`, `docs-plugin/development.md`,
`docs-plugin/management-api.md`.

## 1. Confirm the host binary supports plugins at all

Every Management API response includes:

```
X-CPA-SUPPORT-PLUGIN: 1
```

`1` means the running binary was built with cgo and can load dynamic-library plugins;
`0` means it can't (see `build.md` — a `CGO_ENABLED=0` host binary hits the
`loader_unsupported.go` stub on Linux/macOS/FreeBSD). This header only reports **build**
capability — it says nothing about whether `plugins.enabled` is true or a specific plugin
loaded.

## 2. The management endpoint table

All under `/v0/management`, requiring the management key:

| Method + Path | Purpose |
|---|---|
| `GET /plugins` | Lists discovered, configured, and registered plugins; returns `plugins_enabled`, `effective_enabled`, menus, metadata, config fields. |
| `PATCH /plugins/{pluginID}/enabled` | Updates only `plugins.configs.<pluginID>.enabled`; never the global switch. |
| `GET /plugins/{pluginID}/config` | Gets the preserved configuration object for a plugin. |
| `PUT /plugins/{pluginID}/config` | Replaces the whole plugin configuration object. |
| `PATCH /plugins/{pluginID}/config` | Shallow-merges the configuration object; `null` values delete fields. |
| `DELETE /plugins/{pluginID}` | Target-unloads the plugin, deletes its local dynamic library, removes saved config. |
| `GET /plugin-store` | Lists plugins in the plugin store and their local install state. |
| `POST /plugin-store/{pluginID}/install` | Installs/updates a plugin from the store; use `?source=<sourceID>` when multiple sources share an ID. |

Installing/updating via the store: the host downloads the release asset, verifies
`checksums.txt`, **target-unloads the plugin before overwriting** the dynamic library,
writes the new file, then triggers a config hot reload. If platform/file locks prevent
overwriting an already-loaded library, the endpoint returns a **conflict response that
requires a restart** (this is the Windows shadow-copy lock case — see
`discovery-and-install.md` §5).

## 3. Reading `GET /v0/management/plugins`

```bash
curl -s -H "Authorization: Bearer $MGMT_KEY" \
  http://127.0.0.1:<mgmt-port>/v0/management/plugins | jq
```

Four status fields — do not conflate them:

- `plugins_enabled` — the **global** switch (`plugins.enabled` in `config.yaml`).
- `enabled` — the **individual** plugin's own config switch (`plugins.configs.<id>.enabled`).
- `registered` — the dynamic library has been loaded **and** registration completed
  (`plugin.register` succeeded and passed `validPlugin` checks).
- `effective_enabled` — the actual enabled state after global switch, individual switch,
  and registration state are **all** satisfied. This is the field that tells you the
  plugin is actually live and serving traffic.

Success looks like `registered: true` **and** `effective_enabled: true` for your plugin's
ID.

## 4. Minimal verification flow (from upstream docs)

1. Build the dynamic library for the current platform and place it in
   `plugins/<GOOS>/<GOARCH>/` or flat `plugins/`.
2. Enable `plugins.enabled` in `config.yaml` and add `plugins.configs.<pluginID>`.
3. Start (or hot-reload) CLIProxyAPI.
4. `GET /v0/management/plugins` → confirm `registered: true` and `effective_enabled: true`.
5. If the plugin has resource pages, open `/v0/resource/plugins/<pluginID>/<path>`.
6. If the plugin has its own Management API routes, request the corresponding
   `/v0/management/...` route with the management key.
7. After modifying the plugin binary, install or delete it through the Management API, or
   restart the service — and confirm the *old* dynamic library instance is no longer in
   use (a stale handle otherwise keeps serving under the old code, see
   `discovery-and-install.md` §6).

## 5. Common load failures and where they surface

| Symptom | Cause | Where it shows |
|---|---|---|
| Plugin never appears in `GET /plugins` at all | File not in a scanned directory, wrong extension, or ID regex mismatch (`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`) | nothing — silently absent from the list |
| Appears but `registered: false` | `dlopen`/`dlsym` failed, or `abi_version != 1` | host **logs** (warning), not the API response |
| Appears, `registered: false`, config looks right | Missing metadata — empty `Name`/`Version`/`Author`/`GitHubRepository`, or zero non-nil capabilities | host log: `"plugin %s returned invalid metadata or no capabilities"` |
| Plugin's `plugin.register` response rejected | `SchemaVersion` in the response is **greater** than the host's (`pluginabi.SchemaVersion = 1`) | host log: `"plugin schema version %d is not supported"` |
| A plugin-owned management route silently doesn't respond | Route path conflicts with a higher-priority plugin or a reserved built-in `/v0/management` route | host log: `"pluginhost: plugin %s management route %s conflicts with a higher-priority plugin and was skipped"` |
| Plugin was fine, now stuck `registered: false` after a crash | The plugin panicked and got **fused** (circuit-broken); a fused plugin is never auto-reloaded from the same file path | only recovers by changing the selected file (version bump) or restarting the host — see `discovery-and-install.md` §6 |
| Windows: store install/update returns a conflict | `ErrLoadedPluginLocked` — an already-loaded DLL's real file path can't be overwritten | endpoint responds with a conflict requiring restart |
| Editing the dylib in place has no effect | The host still has the old handle open; editing a file on disk doesn't itself trigger `UnloadPlugin` | use the Management API delete/reinstall, or restart, to actually `dlclose` the stale handle |
| `X-CPA-SUPPORT-PLUGIN: 0` on every response | Host binary built with `CGO_ENABLED=0` | rebuild the host binary with cgo — no plugin will ever load regardless of config |

## 6. After editing a dylib

Recompiling and overwriting the file on disk does **not** by itself unload the previously
loaded instance — the host's config-file watcher will pick up a *config* change (or a
store install/delete will explicitly unload), but simply replacing bytes at the same path
outside those flows can leave a stale handle serving old code until the next full
`ApplyConfig` pass finds a version mismatch or the plugin is explicitly reinstalled/deleted
via the Management API, or the host is restarted.
