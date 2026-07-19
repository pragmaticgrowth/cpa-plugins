# Discovery, placement, versioned filenames, hot-reload/fuse behavior

Pinned to upstream v7.2.88. Source: `internal/pluginhost/platform.go`,
`internal/pluginhost/host.go`, `internal/pluginhost/abi.go`,
`internal/pluginhost/loader_unix.go`, `internal/pluginhost/loader_windows.go`,
`internal/config/plugin_path.go`.

## 1. `plugins.dir` resolution

`internal/config/plugin_path.go`:

```go
const defaultPluginsDir = "plugins"

func ResolvePluginsDir(pluginsDir string) (string, error) {
	pluginsDir = strings.TrimSpace(pluginsDir)
	if pluginsDir == "" {
		pluginsDir = defaultPluginsDir
	}
	if strings.HasPrefix(pluginsDir, "~") {
		homeDir, _ := os.UserHomeDir()
		return filepath.Clean(filepath.Join(homeDir, filepath.FromSlash(normalized))), nil
	}
	return filepath.Clean(pluginsDir), nil
}
```

Empty `plugins.dir` → `./plugins` relative to CWD. A leading `~` expands to
`os.UserHomeDir()`. `(cfg *Config) ResolvePluginsDir()` mutates `cfg.Plugins.Dir` in place
during config load, so downstream code (including `pluginhost.runtimeConfigFromConfig`,
which resolves it again defensively) can assume it's already an absolute/cleaned path.

## 2. Candidate directories — a documented-vs-source discrepancy

The actual Go source (`internal/pluginhost/platform.go`, `candidateDirs`) scans **two**
directories, in this order:

```go
func candidateDirs(root, goos, goarch string) []string {
	dirs := make([]string, 0, 2)
	dirs = append(dirs, filepath.Join(root, goos, goarch)) // e.g. plugins/darwin/arm64
	dirs = append(dirs, root)                                // plugins/ itself (flat layout)
	return dirs
}
```

`internal/pluginstore/install.go` (`pluginCandidateDirs`) and
`internal/homeplugins/sync.go` (`pluginCandidateDirs`) duplicate this exact two-directory
rule, so store installs and host discovery always agree on layout.

**Discrepancy to be aware of:** the upstream docs page (`docs-plugin/development.md`,
"Plugin File Discovery") describes a **third**, higher-priority candidate:

```text
plugins/<GOOS>/<GOARCH>-<variant>
plugins/<GOOS>/<GOARCH>
plugins
```

i.e. a `<GOARCH>-<variant>` directory (e.g. for a CPU-feature variant build) is
documented as searched *before* the plain `<GOARCH>` directory. This is **not** present in
the vendored `candidateDirs` source at v7.2.88 — treat the two-directory behavior above as
ground truth for this pinned version, and don't rely on a `-<variant>` suffix directory
being scanned unless you've confirmed it against the actual running host's source.

So, practically: place a hand-built macOS arm64 plugin at

```
<plugins.dir>/darwin/arm64/<id>.dylib
```

or, as a flat fallback, at `<plugins.dir>/<id>.dylib`.

## 3. File naming / extension / versioning

Extension by OS (`pluginExtension`, duplicated identically in pluginhost, pluginstore,
and homeplugins):

```go
func pluginExtension(goos string) string {
	switch goos {
	case "darwin":
		return ".dylib"
	case "windows":
		return ".dll"
	default:
		return ".so"
	}
}
```

Filenames follow `<id>.<ext>` or a **versioned** form `<id>-v<version>.<ext>`
(`pluginFileFromPath`):

```go
if versionIndex := strings.LastIndex(name, "-v"); versionIndex > 0 {
	candidateID := name[:versionIndex]
	candidateVersion := name[versionIndex+2:]
	if validPluginID(candidateID) && validPluginVersion(candidateVersion) {
		id = candidateID
		version = candidateVersion
	}
}
```

- Plugin ID pattern: `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$` (`pluginIDPattern`).
- Version pattern: `^[0-9][0-9A-Za-z.+-]*$`, and **must not** start with `v`
  (`validPluginVersion`). So on-disk filenames use `-v1.2.3` but the version string used
  internally everywhere is `1.2.3` (no `v`).
- Multiple files for the same `id` may coexist (`foo.so`, `foo-v1.2.0.so`,
  `foo-v2.0.0.so`); the host picks exactly one per `id` at discovery time.
- The plugin **store installer always writes the versioned form**
  (`plugins/{goos}/{goarch}/{id}-v{version}{ext}`), never the bare `{id}{ext}` name — this
  is what lets old versions stick around on disk as rollback candidates.

## 4. Selection algorithm

Entry point: `pluginhost.selectPluginFilesWithCandidates(root, desiredVersions...)`
(public alias `DiscoverPluginFiles`).

1. Build `candidates := candidateDirs(root, runtime.GOOS, runtime.GOARCH)` (§2).
2. For each candidate dir in order, `os.ReadDir`, keep regular files with the platform
   extension, **sort filenames alphabetically**.
3. Parse each into `pluginFile{ID, Path, Version}` (§3).
4. Per `ID`, `pluginFilePreferredForDesired` decides whether a newly-seen file replaces
   the currently "selected" one for that ID:
   - If a **desired version** is pinned for the ID (via `store.version` /
     `store.release-tag` in that plugin's config, see `config-wiring.md`), a file matching
     that exact version always wins over one that doesn't.
   - Otherwise `pluginFilePreferred` picks the file with the **higher semantic version**
     (`comparePluginVersions`, dot-separated numeric segment compare); ties or
     non-numeric versions fall back to lexicographic `>`; an empty version never beats a
     non-empty one.
5. After scanning all candidate dirs, if a desired version is pinned for an ID but the
   winning file's version doesn't match it, that ID is **dropped entirely** from
   selection for this pass.

**Net effect: the newest on-disk version wins by default.** Pinning
`store.version`/`store.release-tag` forces an exact version and excludes the plugin
outright if that exact file isn't present.

### Cleanup of unselected files

`cleanupUnselectedPluginFiles(root, loadedFiles)` removes other files for the same
plugin IDs that were **not** selected (e.g. stale versions from a store upgrade) — but
only runs **once per host lifetime**, gated by `h.cleanupFilesPending` (cleared after the
first successful load pass), so it won't repeatedly rescan/delete on every config touch.

## 5. Loading mechanism (dlopen + C ABI)

Not Go's `plugin.Open` — a custom C ABI, `dlopen`/`dlsym` (POSIX) or
`syscall.LoadDLL` (Windows). `internal/pluginhost/abi.go`:

```go
const pluginHostABIVersion = pluginabi.ABIVersion // = 1

type pluginClient interface {
	Call(ctx context.Context, method string, request []byte) ([]byte, error)
	Shutdown()
}
type pluginLoader interface {
	Open(file pluginFile, host *Host) (pluginClient, error)
}
```

Every plugin must export a single C function, `cliproxy_plugin_init` (see
`${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginapi-types.go` and the `simple` example
README for the full C struct shapes). Host-side open sequence (`dynamicLibraryLoader.Open`,
`loader_unix.go`):

1. `dlopen(path, RTLD_NOW|RTLD_LOCAL)`.
2. `dlsym(handle, "cliproxy_plugin_init")` — error if missing.
3. Allocate a `cliproxy_host_api` + host-context id, register in a package-level
   `sync.Map` so the C callback trampoline can find the right `*Host`/plugin ID.
4. Call `cliproxy_plugin_init(hostAPI, &client.api)`.
5. Verify `client.api.abi_version == pluginHostABIVersion` (currently `1`) — **a mismatch
   rejects the load outright**, not just a warning.
6. Verify `client.api.call != nil && client.api.free_buffer != nil`.

Two independent version numbers (`sdk/pluginabi/types.go`):

```go
const (
	ABIVersion    uint32 = 1 // native C ABI shape — mismatch => Open() fails immediately
	SchemaVersion uint32 = 1 // JSON-RPC contract at plugin.register — newer-than-host is rejected
)
```

`SchemaVersion`: the plugin's `plugin.register` response includes its own
`SchemaVersion`; if **greater** than the host's, `registerRPCPlugin` rejects it
(`"plugin schema version %d is not supported"`) — the host tolerates an older-or-equal
schema version. New RPC capabilities are added via new method names/capability flags
without bumping `SchemaVersion`.

### Windows: shadow-copy loading

Windows Go-built DLLs are not safe to hot-unload from a live process, so
`loader_windows.go` never loads directly from `plugins/<goos>/<goarch>/`. Instead
`shadowCopyPlugin` copies the file into `%TEMP%/cliproxy-pluginhost/pid-<PID>/`, named
`cliproxy-plugin-<id>-<sha256[:32]><ext>`, and `LoadDLL`s **that** copy (content-addressed,
so re-copying is skipped if an identical shadow copy already exists). `Shutdown()`
deliberately does not `dll.Release()` the module but does delete/queue-delete the shadow
file. This is why installing a **new** version over a **loaded** plugin's real file is
blocked on Windows (`ErrLoadedPluginLocked`) — the OS may still hold a handle on the real
path even though the *live* DLL is actually the detached shadow copy.

## 6. Hot-reload (`ApplyConfig`)

`(*Host).ApplyConfig(ctx, cfg)` is the single reconciliation entry point — idempotent,
diffs against in-memory state, guarded by `h.applyMu`. Triggered by:

1. Initial service build (`sdk/cliproxy/builder.go`).
2. **Config-file hot-reload (fsnotify)** — editing `plugins.enabled`/`plugins.configs.<id>`
   on disk takes effect live, no restart.
3. Management API config save (`reloadConfigAfterManagementSave`).
4. Auth add/update/remove events (re-registers plugins so cached auth/model state stays current).
5. Explicit teardown (`ApplyConfig(ctx, &config.Config{})`, i.e. plugins disabled).

Per plugin file selected each pass:

- If `!item.Enabled`, skip entirely — file selection still finds it, but the host never
  `dlopen`s/registers a disabled instance.
- If a **fused** (previously panicked) plugin's file path hasn't changed, it's skipped —
  **the only way to un-fuse a crashed plugin without restarting the host is to change
  which file is selected for it** (bump the on-disk version, or edit its pinned
  `store.version`).
- If the selected **file changed** for an already-loaded ID (version bump on disk): the
  old `*loadedPlugin` is moved to `h.retired[id]` (kept, not yet shut down) while the new
  one is `dlopen`'d and registered; only after the new one is confirmed loaded does the
  old become eligible for `Shutdown()`.
- **First registration** for a freshly-dlopen'd instance calls RPC method
  `plugin.register`; **subsequent** calls (once already registered) call
  `plugin.reconfigure` instead.
- `validPlugin(plugin)` requires non-empty `Name`/`Version`/`Author`/`GitHubRepository`
  metadata **and** at least one non-nil capability, else registration is discarded with a
  warning (`"plugin %s returned invalid metadata or no capabilities"`) — this is a
  *silent* failure from the API's point of view; check host logs.

`Snapshot` is an `atomic.Value` — the entire "which plugins/capabilities are live" view is
replaced atomically at the end of a successful pass, so concurrent requests never see a
half-updated plugin set.

**Important:** flipping `plugins.configs.<id>.enabled: false` alone does **not** unload an
already-running plugin's native library — it only stops the host from re-selecting it on
the *next* `ApplyConfig` pass. The previous instance keeps running with its
last-registered capabilities until something explicitly calls `UnloadPlugin`/`ShutdownAll`.

## 7. Unload / shutdown

```go
func (h *Host) UnloadPlugin(id string) bool {
	// collects BOTH h.loaded[id] and everything in h.retired[id]
	// rebuilds the snapshot WITHOUT this id's records
	// only THEN, outside the lock: Shutdown() every collected client
}
```

`UnloadPlugin(id)` is the **only** path that actually `dlclose`s/`DLL.Release()`s a
plugin's native library. It updates the snapshot *before* calling `Shutdown()`, so
in-flight requests on the old snapshot finish against a still-open handle while new
requests immediately stop seeing the plugin. `guardedPluginClient` wraps every client with
an in-flight-call counter: `Shutdown()` blocks until all in-flight `Call()`s finish before
delegating to the real `dlclose` — a hot-reload swap never races an in-flight call.

## 8. The plugin STORE (distribution, separate concern)

`internal/pluginstore` (re-exported via `sdk/pluginstore`) turns a `registry.json` entry
or a `Manifest` into a file on disk at `plugins/<goos>/<goarch>/{id}-v{version}{ext}` —
**always the versioned filename**. The host doesn't know or care how the file got there;
it just discovers whatever's on disk per §1–4. See `config-wiring.md` for the
`store-sources`/`store-auth` config keys and the `store:` per-plugin pin block.

Install flow highlights (`internal/pluginstore/install.go`):

- `InstallArchive` opens the downloaded zip, requires the target library at the **zip
  root** (not a subdirectory), named `{id}{ext}` or `{id}-v{version}{ext}` — rejects
  multiple candidates or path-traversal names.
- Writes are atomic (`writeFileAtomic`: temp file in the same dir, `fsync`, `os.Rename`).
- If a file already exists at the exact versioned path, a byte-identical write is a no-op
  (`Skipped: true`); a genuinely different write triggers `options.BeforeWrite` then, on
  Windows only, checks `options.PluginLoaded()` and aborts with `ErrLoadedPluginLocked`
  rather than overwrite a mapped DLL.
