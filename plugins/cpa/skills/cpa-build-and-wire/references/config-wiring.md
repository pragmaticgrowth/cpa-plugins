# config.yaml wiring: full `plugins.*` schema

Pinned to upstream v7.2.88. Source: `internal/config/config.go`,
`internal/pluginhost/config.go`, `docs-plugin/development.md`,
`docs-plugin/management-api.md`.

## Go types (`internal/config/config.go`)

```go
// PluginsConfig holds dynamic plugin system settings.
type PluginsConfig struct {
	Enabled      bool                            `yaml:"enabled" json:"enabled"`
	Dir          string                          `yaml:"dir" json:"dir"`
	StoreSources []string                        `yaml:"store-sources,omitempty" json:"store-sources,omitempty"`
	StoreAuth    []sdkpluginstore.AuthConfig      `yaml:"store-auth,omitempty" json:"store-auth,omitempty"`
	AuthRevision int64                           `yaml:"auth-revision,omitempty" json:"auth-revision,omitempty"`
	Configs      map[string]PluginInstanceConfig `yaml:"configs" json:"configs"`
}

// PluginInstanceConfig stores host-owned plugin settings and the original plugin YAML subtree.
type PluginInstanceConfig struct {
	Enabled  *bool     `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Priority int       `yaml:"priority,omitempty" json:"priority,omitempty"`
	Raw      yaml.Node `yaml:"-" json:"-"` // full original YAML subtree, preserved losslessly
}
```

## Full example

```yaml
plugins:
  enabled: true            # GLOBAL master switch — MUST be true or nothing loads
  dir: plugins               # or ~/some/dir; tilde-expanded, resolved relative to CWD if relative
  store-sources:
    - https://example.com/my-registry.json
  store-auth:
    - match: https://example.com/
      type: bearer
      token-env: MY_REGISTRY_TOKEN
      apply-to: [registry, metadata, artifact]
  configs:
    my-plugin:                    # key MUST equal the plugin ID (filename sans ext)
      enabled: true                 # per-instance switch; does NOT flip the global switch
      priority: 10                    # higher wins on route/model/flag conflicts
      # anything else here is plugin-specific and passed through verbatim as
      # ConfigYAML to the plugin's plugin.register / plugin.reconfigure RPC call
      some-plugin-setting: foo
      store:
        # optional: pins this plugin to an exact version/source for auto-install
        version: 1.2.3
        repository: https://github.com/acme/my-plugin
```

## `plugins.enabled` — global switch

`false` by **default**. If `false`, plugin files and per-instance config can still exist
on disk, but nothing becomes effectively enabled — `ApplyConfig` short-circuits: clears
management/resource routes, rebuilds empty active-plugin maps, stores an empty
`Snapshot`. It does **not** unload already-loaded native libraries at that point (see
`discovery-and-install.md` §6–7 for what actually tears things down).

## `plugins.dir` — discovery root

Empty → `./plugins`. Leading `~` → `os.UserHomeDir()`. See `discovery-and-install.md` §1
for exact resolution and §2 for the scanned subdirectory layout.

## `plugins.configs.<id>` — the "Raw passthrough" trick

Custom `UnmarshalYAML`/`MarshalYAML` on `PluginInstanceConfig` means a plugin's config
block is **not** restricted to `enabled`/`priority` — the host only *extracts* those two
keys for its own bookkeeping, but stores the entire original YAML node in `.Raw` and
re-serializes it verbatim on save, so hand-authored comments/ordering/extra keys the host
doesn't understand round-trip losslessly through `config.SaveConfigPreserveComments`.

Missing/absent config node for an `id` defaults to `enabled:false` — **plugins are
opt-in per-instance**, not enabled by default just by dropping a discovered file in.

`runtimeConfigYAML(item, enabled)` deep-copies `item.Raw` and force-ensures the
`enabled`/`priority` scalar keys are present (`ensureMappingScalar`) even if the user's
raw YAML omitted them — this normalized blob (`ConfigYAML`) is exactly what's sent to the
plugin as the RPC request body for `plugin.register`/`plugin.reconfigure`:

```go
type rpcLifecycleRequest struct {
	ConfigYAML    []byte // = the plugin's own YAML subtree, enabled/priority guaranteed present
	SchemaVersion uint32 // = pluginabi.SchemaVersion (host's own, currently 1)
}
```

The host does not interpret plugin-specific keys at all beyond `enabled`/`priority` — the
plugin parses its own `ConfigYAML` bytes. If the raw node is entirely empty, the fallback
payload sent is `"enabled: false\npriority: 0\n"`.

## `plugins.configs.<id>.priority` — ordering & routing semantics

```go
func sortRecords(records []capabilityRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].priority == records[j].priority {
			return records[i].id < records[j].id
		}
		return records[i].priority > records[j].priority
	})
}
```

**Higher `priority` sorts first**; ties broken alphabetically by plugin ID. Governs:
iteration order of `activeRecords()`/`RegisteredPlugins()`; management/resource route
registration order (a **lower-priority plugin's route is silently dropped with a warning
log** if a higher-priority plugin or reserved built-in route already claimed the same
`METHOD path` key); model routing candidates and other "first plugin wins" capabilities.

Also: **executors require a matching auth record with the same provider key**; if the
same provider is *also* configured as OpenAI-compatible, the **native executor wins** over
a plugin executor. Native flags/routes and higher-priority plugin flags/routes can never
be replaced by a lower-priority plugin.

## `plugins.configs.<id>.enabled` — per-instance enable

`item.Enabled` is a `*bool`, nil normalized to `false` on parse:

```go
item, ok := rc.Items[file.ID]
if !ok {
	item = defaultRuntimeItemConfig(file.ID) // Enabled:false
}
if !item.Enabled {
	continue // plugin file is skipped entirely this pass
}
```

A binary can sit in `plugins/` and never be touched until an operator adds
`plugins.configs.<id>: {enabled: true}` — discovery finds the file regardless, the host
only `dlopen`s+registers entries whose config item is enabled. Flipping `enabled: false`
alone does **not** unload an already-running instance — see `discovery-and-install.md` §6.

## `plugins.configs.<id>.store` — pinning a version for the plugin store

Read via `pluginConfigDesiredVersion(item)`, which looks at `item.Raw.store.version`
(falls back to `item.Raw.store.release-tag`) to build the pinned-version map that feeds
file selection (`discovery-and-install.md` §4). Only relevant if you're using the plugin
store to install/auto-update; a hand-placed dylib doesn't need a `store:` block at all.

## `plugins.store-sources` / `plugins.store-auth`

- `StoreSources []string` — additional plugin **registry URLs**, each becoming a
  `pluginstore.Source`, beyond the built-in official one:
  ```text
  https://raw.githubusercontent.com/router-for-me/CLIProxyAPI-Plugins-Store/main/registry.json
  ```
- `StoreAuth []sdkpluginstore.AuthConfig` — auth rules applied when the **host itself**
  fetches from plugin store URLs (registry/metadata/artifact), matched by URL prefix and
  optionally scoped via `apply-to: [registry, metadata, artifact]`. Auth `Type`s: `none`,
  `bearer` (`Authorization: Bearer <token>`), `basic` (base64 `user:pass`), `header`
  (arbitrary header name/value), `github-token` (bearer, semantically for
  `api.github.com`). References an **env var name** (`token-env`, `username-env`,
  `password-env`, `header-value-env`) — the actual secret is read from the environment at
  request time and never written to disk.
- `AuthRevision int64` — bumped when Home-managed plugin credentials change; only a
  cache-invalidation signal, not consumed by the plugin host directly.

## Editing config wiring live via the Management API

Instead of hand-editing `config.yaml`, the same fields are reachable at runtime (all
under `/v0/management`, all require the management key):

| Method + Path | Effect |
|---|---|
| `PATCH /plugins/{pluginID}/enabled` | updates only `plugins.configs.<id>.enabled` — never the global switch |
| `GET /plugins/{pluginID}/config` | reads the preserved config object for a plugin |
| `PUT /plugins/{pluginID}/config` | replaces the whole plugin config object |
| `PATCH /plugins/{pluginID}/config` | shallow-merges; `null` values delete fields |

Both paths (file edit + hot-reload watcher, or Management API write) converge on the same
`ApplyConfig` reconciliation — see `verify.md` for the full endpoint table and
`discovery-and-install.md` §6 for what triggers a reconciliation pass.
