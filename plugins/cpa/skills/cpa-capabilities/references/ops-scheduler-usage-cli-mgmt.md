# Scheduler, Usage, CLI, Management API, Host Callbacks — depth reference

Covers: `scheduler`, `usage_plugin`, `command_line_plugin`, `management_api`, and host callbacks
(`host.*` — always available, not a capability flag).

Ground truth: `${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginapi-types.go` (1322 lines — all Go
interface/struct definitions plugin authors implement or exchange over RPC),
`${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginabi-types.go` (wire-level method name constants
and envelope shape). Per-capability docs under `${CLAUDE_PLUGIN_ROOT}/references/upstream/docs-plugin/`.
Pinned to upstream **v7.2.88** (module `github.com/router-for-me/CLIProxyAPI/v7`).

All five capabilities are RPC method groups on top of the same C ABI plugin transport: a plugin
exports `cliproxy_plugin_init`/`cliproxyPluginCall`/`cliproxyPluginFree`/`cliproxyPluginShutdown`
(`pluginabi.ABIVersion = 1`), and the host calls JSON-RPC-shaped methods identified by string
constants in `pluginabi` (e.g. `scheduler.pick`, `usage.handle`, `command_line.register`,
`management.handle`, `host.http.do`). Every request/response is wrapped in an
`Envelope{OK, Result, Error}`.

---

## 1. Scheduler capability

### Purpose

Lets a plugin choose which credential (`AuthID`) the host should use for a request, or explicitly
hand the decision to a named built-in scheduler, **before** the host's built-in scheduler runs.
This is a pre-routing hook over auth selection, not over model routing (that's the separate
`model_router`/`model.route` capability — see
`${CLAUDE_PLUGIN_ROOT}/skills/cpa-capabilities/references/providers-auth-exec.md`).

### Capability field

```json
{ "capabilities": { "scheduler": true } }
```

### Go interface

```go
// sdk/pluginapi/types.go
type Scheduler interface {
    Pick(context.Context, SchedulerPickRequest) (SchedulerPickResponse, error)
}
```

### Method

| Method | Constant | Purpose |
|---|---|---|
| `scheduler.pick` | `pluginabi.MethodSchedulerPick` | Ask the plugin to pick an auth candidate for one request. |

### Request/response types

```go
type SchedulerPickRequest struct {
    Plugin     Metadata               // injected by host before the call
    Provider   string                 // primary provider key requested by the route
    Providers  []string               // every provider key accepted by the route
    Model      string
    Stream     bool
    Options    SchedulerOptions       // Headers map[string][]string; Metadata map[string]any
    Candidates []SchedulerAuthCandidate
}

type SchedulerAuthCandidate struct {
    ID         string
    Provider   string
    Priority   int
    Status     string
    Attributes map[string]string      // immutable routing/provider attributes
    Metadata   map[string]any         // mutable host-managed auth metadata
}

type SchedulerPickResponse struct {
    AuthID          string  // selected credential ID
    DelegateBuiltin string  // "round-robin" | "fill-first"
    Handled         bool    // false => host falls through to next plugin / built-in logic
}

const (
    SchedulerBuiltinRoundRobin = "round-robin"
    SchedulerBuiltinFillFirst  = "fill-first"
)
```

### Wire example

```json
{
  "Provider": "codex",
  "Providers": ["codex"],
  "Model": "gpt-5.5",
  "Stream": true,
  "Options": { "Headers": {}, "Metadata": {} },
  "Candidates": [
    { "ID": "auth-1", "Provider": "codex", "Priority": 1, "Status": "available", "Attributes": {}, "Metadata": {} }
  ]
}
```

Responses — select a specific credential:

```json
{ "AuthID": "auth-1", "Handled": true }
```

Delegate to a built-in scheduler:

```json
{ "DelegateBuiltin": "round-robin", "Handled": true }
```

Do not handle:

```json
{ "Handled": false }
```

### Host integration (`internal/pluginhost/scheduler.go`)

`Host.PickAuth(ctx, req)` is the entry point the core router calls. It looks up
`h.schedulerRecord()` — **the first active plugin record (by priority order) whose
`Capabilities.Scheduler != nil`**, i.e. only one plugin's scheduler runs per request even if
several plugins declare the capability:

```go
func (h *Host) schedulerRecord() *capabilityRecord {
    for _, record := range h.activeRecords() {
        if h.isPluginFused(record.id) || record.plugin.Capabilities.Scheduler == nil {
            continue
        }
        return &record
    }
    return nil
}
```

`activeRecords()` is sorted descending by `priority`, ties broken by ascending plugin ID string
(higher `plugins.configs.<id>.priority` wins and runs first — opposite of "lower number = higher
precedence"). `h.callScheduler` sets `req.Plugin = record.meta` then calls `scheduler.Pick(ctx,
req)`, recovering panics into "fusing" the plugin. A panicking scheduler plugin gets permanently
disabled ("fused") for the remaining process lifetime.

The response is validated by `normalizeSchedulerResponse`:
- Must set either `AuthID` (must exist in `req.Candidates`) or `DelegateBuiltin` (must be
  `round-robin`/`fill-first`) when `Handled=true`; otherwise the pick is treated as invalid and
  discarded (falls back as if `Handled: false`).
- Returning an `error` from `Pick` propagates as a **failed** scheduling attempt (visible to
  caller), distinct from `Handled:false` which just lets later plugins / built-in logic continue.

### Config

```yaml
plugins:
  configs:
    scheduler:
      enabled: true
      priority: 1
      auth_id: ""
      delegate: ""
      deny: false
```

`auth_id`/`delegate`/`deny` here are **plugin-defined config fields**, not host schema. Only
`enabled`/`priority` are host-reserved keys; everything else under a plugin's config block is
passed through as raw YAML for the plugin to parse itself with `gopkg.in/yaml.v3`.

Example plugin behavior: `deny: true` → return an error; `delegate` of `fill-first`/`round-robin`
→ delegate to the built-in scheduler; `auth_id` non-empty and present in the candidate list →
select that credential.

### Full worked example

```go
func pickAuth(raw []byte) ([]byte, error) {
    var req pluginapi.SchedulerPickRequest
    json.Unmarshal(raw, &req)
    cfg := loadedConfig()
    if cfg.Deny {
        return errorEnvelope("scheduler_denied", "scheduler pick denied by plugin configuration"), nil
    }
    switch cfg.Delegate {
    case pluginapi.SchedulerBuiltinFillFirst, pluginapi.SchedulerBuiltinRoundRobin:
        return okEnvelope(pluginapi.SchedulerPickResponse{DelegateBuiltin: cfg.Delegate, Handled: true})
    case "":
    default:
        return okEnvelope(pluginapi.SchedulerPickResponse{Handled: false})
    }
    if cfg.AuthID == "" {
        return okEnvelope(pluginapi.SchedulerPickResponse{Handled: false})
    }
    for _, candidate := range req.Candidates {
        if candidate.ID == cfg.AuthID {
            return okEnvelope(pluginapi.SchedulerPickResponse{AuthID: cfg.AuthID, Handled: true})
        }
    }
    return okEnvelope(pluginapi.SchedulerPickResponse{Handled: false})
}
```

### Development notes / gotchas

- Only return IDs that appear in `req.Candidates` — the host actively rejects unknown `AuthID`s.
- Only one scheduler plugin effectively runs per request (first by priority order). If you need
  coexistence with other scheduler-capable plugins, use `Handled: false` to defer.
- Errors from `Pick` fail the scheduling attempt outright — use this only for deliberate denial
  (like the `deny` config flag in the example), not for "I don't want to handle this" (use
  `Handled: false` for that).
- Panics fuse (permanently disable) the plugin for the process lifetime — don't let a bad candidate
  list or nil map crash your handler.

### Vendored example

`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/scheduler/go/main.go` — the dedicated
single-purpose example above; `"Capabilities": registrationCapability{Scheduler: true, ...}`.

---

## 2. Usage Observer capability (`usage_plugin`)

### Purpose

A side-channel, fire-and-forget observer that receives one `UsageRecord` per completed request
(success or failure) — token counts, latency, TTFT, billing metadata. Intended for external
stats/billing/audit systems, not for altering requests or responses.

### Capability field

```json
{ "capabilities": { "usage_plugin": true } }
```

### Go interface

```go
type UsagePlugin interface {
    HandleUsage(context.Context, UsageRecord)
}
```

Note this returns nothing — it's not even allowed to signal failure back to the host; any error
handling must happen inside the plugin (buffer, retry, log — but don't block).

### Method

| Method | Constant | Purpose |
|---|---|---|
| `usage.handle` | `pluginabi.MethodUsageHandle` | Deliver one completed-request usage record. |

### `UsageRecord`

```go
type UsageRecord struct {
    Provider        string
    ExecutorType    string
    Model           string
    Alias           string          // user-facing model alias, if used
    APIKey          string          // client API key identifier
    AuthID          string          // selected credential ID
    AuthIndex       string
    AuthType        string
    Source          string          // request source/integration
    ReasoningEffort string
    ServiceTier     string
    Generate        bool            // host normalizes omitted values to true
    RequestedAt     time.Time
    Latency         time.Duration
    TTFT            time.Duration   // time to first token, streaming only
    Failed          bool
    Failure         UsageFailure    // {StatusCode int; Body string}
    Detail          UsageDetail     // {InputTokens, OutputTokens, ReasoningTokens, CachedTokens, TotalTokens int64}
    ResponseHeaders http.Header
}
```

Wire example:

```json
{
  "Provider": "codex",
  "ExecutorType": "codex",
  "Model": "gpt-5.5",
  "Alias": "gpt-5.5",
  "APIKey": "client-key-id",
  "AuthID": "auth-1",
  "AuthIndex": "0",
  "AuthType": "oauth",
  "Source": "openai",
  "ReasoningEffort": "high",
  "ServiceTier": "priority",
  "RequestedAt": "2026-06-15T12:00:00Z",
  "Latency": 1234567890,
  "TTFT": 120000000,
  "Failed": false,
  "Detail": { "InputTokens": 10, "OutputTokens": 20, "ReasoningTokens": 0, "CachedTokens": 0, "TotalTokens": 30 },
  "ResponseHeaders": {}
}
```

Failed requests include `Failed: true` and `Failure`:

```json
{ "Failure": { "StatusCode": 429, "Body": "rate limited" } }
```

### Host integration (`internal/pluginhost/adapters.go`)

`Host.RegisterUsagePlugins()` iterates `activeRecords()` and, for every record whose
`Capabilities.UsagePlugin != nil`, registers a `*usageAdapter` under the internal usage-fanout
registry with key `"plugin:" + record.id`:

```go
func (h *Host) RegisterUsagePlugins() {
    for _, record := range h.activeRecords() {
        plugin := record.plugin.Capabilities.UsagePlugin
        if plugin == nil || h.isPluginFused(record.id) {
            continue
        }
        coreusage.RegisterNamedPlugin("plugin:"+record.id, &usageAdapter{host: h, pluginID: record.id, plugin: plugin})
    }
}
```

Unlike the scheduler, **all** enabled usage-plugin-capable plugins get registered — there is no
single-winner selection; every one receives every usage record. `RegisterUsagePlugins()` is
invoked once at service startup, after the plugin snapshot is built.
`(*usageAdapter).HandleUsage` translates the host's internal `coreusage.Record` into the public
`pluginapi.UsageRecord` and calls your plugin's `HandleUsage`, wrapped in the same
panic-recovers-to-fuse pattern used elsewhere.

### Minimal example

```go
case "usage.handle":
    return okEnvelopeJSON("{}")
```

### Development notes / gotchas

- Return quickly — `HandleUsage` runs on the request-completion path; slow plugins add latency to
  every request even though the return value is discarded.
- If you must write to an external system (DB, HTTP, queue), buffer/queue inside the plugin and
  flush asynchronously — do not block on outbound I/O in `HandleUsage`.
- Don't leak `APIKey`, `AuthID`/credentials, or full response bodies in whatever you persist/log.
- A panic here fuses the plugin (same as every other capability) — after that, it silently stops
  receiving further usage records for the rest of the process lifetime, with no automatic un-fuse
  short of a plugin reload.
- All usage-capable plugins run for every record (no priority-based single-winner behavior like
  scheduler) — if you're building multiple usage plugins be aware they're independent listeners,
  not a chain.

### Vendored example

`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/usage/go/main.go` — the dedicated
single-purpose example, `"capabilities":{"usage_plugin":true}`.

---

## 3. Command Line Extension capability (`command_line_plugin`)

### Purpose

Lets a plugin add its own CLI flags to the `cli-proxy-api` binary and run custom logic (login
flows, credential import/export, diagnostics) when the user passes one of those flags. Not for
long-running server tasks — it's a one-shot "run then exit" hook wired into the process's flag
parsing / startup path.

### Capability field

```json
{ "capabilities": { "command_line_plugin": true } }
```

### Go interface

```go
type CommandLinePlugin interface {
    RegisterCommandLine(context.Context, CommandLineRegistrationRequest) (CommandLineRegistrationResponse, error)
    ExecuteCommandLine(context.Context, CommandLineExecutionRequest) (CommandLineExecutionResponse, error)
}
```

### Methods

| Method | Constant | Purpose |
|---|---|---|
| `command_line.register` | `pluginabi.MethodCommandLineRegister` | Declare the flags this plugin owns. |
| `command_line.execute` | `pluginabi.MethodCommandLineExecute` | Run when one of the plugin's own flags was set on the command line. |

### Flag registration types

```go
type CommandLineFlag struct {
    Name         string // no leading dashes
    Usage        string // shown in -help
    Type         string // "bool" | "string" | "int" | "int64" | "float64" | "duration"
    DefaultValue string // parsed according to Type
}

type CommandLineFlagValue struct {
    Name, Type, Value string
    Set                bool  // true if user explicitly passed it
}
```

Registration wire example:

```json
{ "Flags": [ { "Name": "plugin-example-command", "Usage": "Run the example plugin command", "Type": "bool", "DefaultValue": "false" } ] }
```

### Execution request/response

```go
type CommandLineExecutionRequest struct {
    Plugin         Metadata
    Program        string                            // os.Args[0]
    Args           []string                           // every CLI arg after Program
    ConfigPath     string                             // effective host config path
    Host           HostConfigSummary                  // AuthDir, ProxyURL, ForceModelPrefix, OAuthModelAlias, ExcludedModels
    Flags          map[string]CommandLineFlagValue     // ALL registered flags visible to host, not just this plugin's
    TriggeredFlags map[string]CommandLineFlagValue     // only the flags owned by this plugin that were set
}

type CommandLineExecutionResponse struct {
    Stdout   []byte
    Stderr   []byte
    Auths    []AuthData // credential records created by the command; host persists them
    ExitCode int        // non-zero affects process exit code
}
```

### Host integration (`internal/pluginhost/command_line.go`)

**Registration** (`Host.RegisterCommandLineFlags(ctx, flagSet)`):
- For each active, non-fused plugin with `Capabilities.CommandLinePlugin != nil`, calls
  `RegisterCommandLine` and, for each returned flag, calls `h.registerCommandLineFlag`.
- Validation performed by the host before wiring the flag into Go's `flag.FlagSet`:
  - `validCommandLineFlagName`: non-empty, no leading `-`, not `help`/`h`, no whitespace/`=`.
  - `normalizeCommandLineFlagType`: must be one of `bool|string|int|int64|float64|duration`
    (case-insensitive; empty defaults to `bool`).
  - `normalizeCommandLineFlagValue` parses the declared default per-type (bool via
    `strconv.ParseBool`, duration via `time.ParseDuration`, etc.) — an invalid default value
    causes the flag to be **skipped with a warning**, not a hard failure.
  - **Collision policy**: if `flagSet.Lookup(name) != nil` (already an existing host flag) OR the
    name was already claimed by a higher-priority plugin, the flag is skipped with a warning.
    First-registered-by-priority-order wins ties.

**Execution** (`Host.ExecuteCommandLine(ctx, program, args, configPath, flagSet)`):
- Called after normal flag parsing. Returns `(0, false)` immediately if **no** plugin-owned flag
  was set — i.e. the host only runs this path when at least one plugin flag was triggered
  (`h.HasTriggeredCommandLineFlags()` gates whether this path runs at all vs normal server start).
- For every active plugin with triggered flags, calls `ExecuteCommandLine` with both `Flags` (all)
  and `TriggeredFlags` (just this plugin's triggered ones), passing `Host: h.hostConfigSummary()`.
- On success with `ExitCode == 0` and non-empty `Auths`, `h.persistCommandLineAuths` uses the
  SDK's `sdkAuth.GetTokenStore()` to `Save()` each returned `AuthData` record to disk (in
  `HostConfigSummary.AuthDir`), then appends `"Authentication saved to <path>\n"` lines to stdout.
- `Stdout`/`Stderr` from every triggered plugin are written directly to the process's real
  `os.Stdout`/`os.Stderr`; a non-zero `ExitCode` from any plugin propagates to the overall process
  exit code (first non-zero wins, further failures don't override it).
- Multiple plugins can be triggered in the same invocation (if the user passes flags belonging to
  different plugins at once) — they all execute in priority order and their outputs are
  concatenated.

### Working example

```go
case "command_line.register":
    return okEnvelopeJSON(`{"Flags":[{"Name":"example-cli-go-command","Usage":"Run the example plugin command","Type":"bool"}]}`)
case "command_line.execute":
    // Stdout is base64: "example-cli-go command executed\n"
    return okEnvelopeJSON(`{"Stdout":"ImV4YW1wbGUtY2xpLWdvIGNvbW1hbmQgZXhlY3V0ZWRcXG4i","ExitCode":0}`)
```

Note: RPC-transported `[]byte` fields (`Stdout`, `Stderr`, `Body`) are base64-encoded JSON strings
at the wire level, matching Go's `encoding/json` default for `[]byte`.

### Development notes / gotchas

- Flag names are global to the whole binary — pick a plugin-prefixed name (`my-plugin-do-x`) to
  avoid silent collisions/skips with host flags or other plugins.
- This path is for one-shot CLI actions (login, credential import, diagnostics) — it runs, writes
  to stdout/stderr, sets an exit code, and the process typically exits; it is not a place to start
  background servers.
- Returning `Auths` is the sanctioned way to get freshly-created credentials persisted to the
  host's auth store from a CLI command (e.g. an OAuth login flow driven interactively via
  stdin/stdout inside `ExecuteCommandLine`).
- A non-zero `ExitCode` really does set the process exit code — treat it like a normal CLI tool's
  contract.

### Vendored example

`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/cli/go/main.go` — the dedicated
single-purpose example above, `"capabilities":{"command_line_plugin":true}`.

---

## 4. Management API capability (`management_api`)

### Purpose

Lets a plugin register (a) authenticated JSON API routes under the host's Management API, and (b)
unauthenticated browser-navigable "resource" pages (status/diagnostics/config UIs) under a
plugin-namespaced path. This is the mechanism for building an admin UI panel or programmatic
control surface for your plugin.

### Capability field

```json
{ "capabilities": { "management_api": true } }
```

### Go interfaces

```go
type ManagementAPI interface {
    RegisterManagement(context.Context, ManagementRegistrationRequest) (ManagementRegistrationResponse, error)
}

type ManagementHandler interface {
    HandleManagement(context.Context, ManagementRequest) (ManagementResponse, error)
}
```

Note `ManagementRoute`/`ResourceRoute` each carry their own `Handler ManagementHandler` — routing
dispatch inside the plugin is up to plugin code (there's no separate "handle" registration call
per se; the *handler* is attached at registration time). Over the wire (native C-ABI plugins),
`Handler` fields aren't directly transportable — in practice a single `management.handle` RPC
method receives every request and the plugin's own `handleMethod` switch dispatches by
`req.Path`/`req.Method` internally (see the vendored examples below, which route entirely inside
one `management.handle` case).

### Registration request/response

```go
type ManagementRegistrationRequest struct {
    Plugin           Metadata
    BasePath         string // always "/v0/management" — the only prefix plugins may register under
    ResourceBasePath string // "/v0/resource/plugins/<pluginID>"
}

type ManagementRegistrationResponse struct {
    Routes    []ManagementRoute  // authenticated JSON API routes
    Resources []ResourceRoute    // unauthenticated browser resource pages
}

type ManagementRoute struct {
    Method, Path string
    Menu, Description string // GET + non-empty Menu => host migrates this to a Resource (see below)
    Handler ManagementHandler
}

type ResourceRoute struct {
    Path, Menu, Description string
    Handler ManagementHandler
}
```

### Request/response for a matched call

```go
type ManagementRequest struct {
    Method  string
    Path    string
    Headers http.Header
    Query   url.Values
    Body    []byte
}

type ManagementResponse struct {
    StatusCode int          // 0 => defaults to 200
    Headers    http.Header
    Body       []byte
}
```

On the wire, `management.handle` actually transports `rpcManagementRequest{ pluginapi.ManagementRequest;
HostCallbackID string `json:"host_callback_id,omitempty"` }` — **the plugin must read
`host_callback_id` off the raw request JSON** (it's not on the public `ManagementRequest` Go
struct) and forward it into any nested `host.model.*` calls it makes from inside the handler (see
§5, "Recursion Guard").

### Route types and auth boundary

| Type | Registration field | Exposed path | Auth |
|---|---|---|---|
| Plugin-owned Management API | `Routes` | `/v0/management/...` | Requires the management key (`Authorization: Bearer <management-key>`). |
| Browser resource page | `Resources` | `/v0/resource/plugins/<pluginID>/...` | **Not** management-authenticated on the GET itself. In same-origin Management Center deployments, trusted page JS *may* read the stored management key from `localStorage` and call `/v0/management/...` itself. |

### Host integration (`internal/pluginhost/management.go`)

- `Host.RegisterManagementRoutes(ctx, reserved)` rebuilds two maps: `h.managementRoutes` and
  `h.resourceRoutes`, keyed by `"METHOD PATH"`. Called with `reserved` = every already-registered
  `/v0/management/*` gin route on the server, meaning **plugins cannot ever override an existing
  host management route**; conflicting routes are dropped with a warning.
- Path normalization: `Method` defaults to `GET`, uppercased, must not contain whitespace. `Path`
  gets `/`-prefixed, `managementBasePath ("/v0/management")` prefix stripped if present (so you can
  pass either `/plugins/example/run` or the fully-qualified form), trailing `/` trimmed. Rejects
  paths containing whitespace, `:`, or `*`.
- **Legacy migration**: any `Routes` entry that is `GET` **and** has a non-empty `Menu` field is
  silently converted into a `ResourceRoute` instead of a management route — this exists
  specifically so old plugins that registered menu pages as GET management routes don't
  accidentally expose an unauthenticated menu page as if it were management-API-protected.
- Resource path normalization: must resolve under `/v0/resource/plugins/<pluginID>/`; rejects
  whitespace, `:`, `*`, and `..` (path traversal guard). Legacy `/plugins/<pluginID>/...` prefix is
  also accepted and rewritten.
- Dispatch: `Host.ServeManagementHTTP(w, r)` — looked up by exact `"METHOD PATH"` in
  `h.managementRoutes`; reads and re-buffers the body, calls `callManagementHandler` →
  `record.route.Handler.HandleManagement(ctx, ManagementRequest{...})`, then runs the response body
  through `escapeManagementResponseBody` (`htmlsanitize.JSONBodyIfLikely`) before writing
  headers/status/body to the `ResponseWriter`. `Host.ServeResourceHTTP(w, r)` — same shape but
  GET-only, looked up in `h.resourceRoutes`, **no HTML sanitization pass applied** to the response
  body (contrast with the management path) — meaning a resource `ManagementHandler` is trusted to
  produce safe HTML itself. Both paths wrap the handler call in the standard
  panic-recovers-to-fuse pattern.

### Worked minimal example

```go
case "management.register":
    return okEnvelopeJSON(`{"resources":[{"Path":"/status","Menu":"Management API","Description":"..."}]}`)
case "management.handle":
    return okEnvelopeJSON(`{"StatusCode":200,"Headers":{"content-type":["text/html; charset=utf-8"]},"Body":"<base64 html>"}`)
```

Final resolved resource URL for a plugin whose loaded ID is `example`:
`/v0/resource/plugins/example/status`.

### Trusted resource page pattern

For privileged actions:
1. Serve the plugin UI as a `ResourceRoute` (unauthenticated GET, HTML/JS).
2. Let that page's JS read the same-origin Management Center's stored management key from
   `localStorage` (only works same-origin; cross-origin deployments must handle its absence).
3. Have the JS call your plugin's own `/v0/management/...` route with `Authorization: Bearer
   <management-key>` for the actual privileged action.

**Do not** wire privileged actions (config writes, credential file reads, host callback
invocations) directly to an unauthenticated resource GET + query params — see the Gotchas section
below; the shipped `host-callback-auth-files` example intentionally violates this guidance for
demo purposes.

### Development notes / gotchas

- Plugin management routes can never win against an existing host `/v0/management` route — check
  the real route table if your route silently doesn't appear.
- Resource paths and management paths reject `:` `*` `..` and whitespace outright.
- A `GET` `ManagementRoute` with a non-empty `Menu` is auto-demoted to a `ResourceRoute` — if you
  actually want an authenticated GET endpoint, do not set `Menu`.
- Response bodies from `/v0/management/...` get an HTML-escaping safety pass; resource-page bodies
  do not — you're on your own to avoid XSS in resource HTML.
- **Real risk in the shipped examples**:
  `${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/host-callback-auth-files/go/main.go`
  demonstrates `GET /v0/resource/plugins/host-callback-auth-files/status?op=save&name=...&json=...`
  — an unauthenticated GET that calls `host.auth.save` and writes a credential file. This directly
  contradicts the "do not bind sensitive actions to unauthenticated resource GET requests"
  guidance. Treat it as a demo-only anti-pattern, not a template to copy for real
  credential-writing UIs.

### Vendored examples

`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/management-api/go/main.go` — the dedicated
single-purpose example, `"capabilities":{"management_api":true}`. Also
`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/host-callback-auth-files/go/main.go` and
`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/host-model-callback/go/main.go` — both drive
their host callbacks from a `management_api` resource page (see §5).

---

## 5. Host Callbacks (not a capability flag — always available)

### Purpose

The reverse-direction RPC: a loaded plugin calls **into** the host to reuse host-managed HTTP
transport, host model execution, host credential file I/O, host streaming/logging. These are not
gated by any capability flag in `plugin.register` — any loaded plugin can invoke them, but they
matter most to Executor, Management API, and credential/resource-page plugins.

### Method groups

**HTTP:**
| Method | Purpose |
|---|---|
| `host.http.do` | Non-streaming HTTP request through the host's managed transport (respects host proxy settings, request logging). |
| `host.http.do_stream` | Streaming HTTP request; returns a `stream_id`. |
| `host.http.stream_read` | Read next chunk of a held HTTP stream. |
| `host.http.stream_close` | Close a held HTTP stream. |

**Model execution:**
| Method | Purpose |
|---|---|
| `host.model.execute` | Non-streaming model request through the host's executor path (`Stream` must be `false`, else `"host.model.execute requires stream=false"` error). |
| `host.model.execute_stream` | Streaming model request; returns `stream_id`. |
| `host.model.stream_read` | Read a chunk of a held model stream. |
| `host.model.stream_close` | Close a held model stream. |

Request shape (`HostModelExecutionRequest`):

```json
{
  "entry_protocol": "openai",
  "exit_protocol":  "openai",
  "model": "gpt-5.5",
  "stream": false,
  "body": "<base64 request body>",
  "headers": {},
  "query": {},
  "alt": ""
}
```

Real dispatch (`internal/pluginhost/host_callbacks.go`, `callHostModelExecute`): resolves
`h.currentModelExecutor()`, translates into `handlers.ModelExecutionRequest` and calls
`executor.ExecuteModel(ctx, ...)`. Errors are surfaced from `interfaces.ErrorMessage` (status/body
preserved).

**Credential file:**
| Method | Purpose |
|---|---|
| `host.auth.list` | List host credential records. |
| `host.auth.get` | Read the physical credential JSON file by auth index. |
| `host.auth.get_runtime` | Read runtime credential info by auth index (not the raw file). |
| `host.auth.save` | Write credential JSON + upsert the runtime credential record. |

**Stream bridge / logging:**
| Method | Purpose |
|---|---|
| `host.stream.emit` | Executor plugin pushes a streaming chunk to the host. |
| `host.stream.close` | Executor plugin closes its stream. |
| `host.log` | Write through the host's `logrus` logger — supports `level` (`trace/info/warn/error`, default `debug`), `message`, `fields`. |

### `host_callback_id` — the recursion guard

When a plugin invokes `host.model.*` **from inside a host-invoked context** (e.g. its own
`management.handle`), it must forward that request's `host_callback_id` (present as a sibling JSON
field alongside the RPC payload — e.g. `rpcManagementRequest.HostCallbackID` — but *not* on the
public `pluginapi.ManagementRequest` struct, so read it off the raw wire JSON) into the nested
`host.model.execute`/`execute_stream` call's own `HostCallbackID` field:

```go
type rpcHostModelExecutionRequest struct {
    pluginapi.HostModelExecutionRequest
    HostCallbackID string `json:"host_callback_id,omitempty"`
}
```

Host-side mechanism (`internal/pluginhost/callback_contexts.go`): every host-invoked RPC
(management handler, resource handler, auth flows, executor calls, etc.) opens a
`callbackContextRegistry` entry (`h.openCallbackContextForPlugin(ctx, pluginID)`) tagging the
`context.Context` with the originating plugin ID and returning an opaque numeric string ID. That
ID is what's threaded through as `host_callback_id`. On the nested `host.model.execute*` call,
`h.callbackCallerPluginID(ctx, req.HostCallbackID)` resolves back to the originating plugin ID and
is passed as `SkipInterceptorPluginID`/`SkipRouterPluginID` on the nested
`handlers.ModelExecutionRequest` — **this makes the host skip that same plugin's own
request/response/stream interceptors on the nested call, preventing infinite recursion.** Other
enabled plugins' interceptors still run on the nested request.

If a plugin **doesn't** forward `host_callback_id` on a streaming call, and never explicitly calls
`host.model.stream_close`, the stream leaks until the callback-context scope closes: when
forwarded, the host auto-closes the stream once the outer RPC (e.g. `management.handle`) returns;
explicit close via `host.model.stream_close` remains the recommended pattern for normal code.

### Worked example: calling from a resource handler

```go
case "management.handle":
    callHost("host.log", []byte(`{"level":"info","message":"...","fields":{"plugin":"example-host-callback-go"}}`))
    callHost("host.http.do", []byte(`{"method":"GET","url":"https://example.com","headers":{"user-agent":["example-host-callback-go"]}}`))
    return okEnvelopeJSON(`{"StatusCode":200, ...}`)
```

```go
// call_host_api C shim wired through cliproxy_host_api.call, stored at plugin init:
func callHost(method string, payload []byte) {
    cMethod := C.CString(method)
    defer C.free(unsafe.Pointer(cMethod))
    var response C.cliproxy_buffer
    var req *C.uint8_t
    if len(payload) > 0 {
        req = (*C.uint8_t)(C.CBytes(payload))
        defer C.free(unsafe.Pointer(req))
    }
    if C.call_host_api(cMethod, req, C.size_t(len(payload)), &response) == 0 && response.ptr != nil {
        C.free_host_buffer(response.ptr, response.len)
    }
}
```

The host API pointer (`stored_host`) is captured in `cliproxy_plugin_init(host *C.cliproxy_host_api,
...)` — **this is the mechanism by which a native plugin gets a callable handle back into the
host**; it's set up once at load time and reused for every `host.*` call.

### Fuller worked example: `host.model.execute`/`execute_stream` from a query-driven resource page

```go
func executeOnce(opts runOptions) (pluginapi.HostModelExecutionResponse, error) {
    body, _ := modelRequestBody(opts)
    result, errCall := callHost(pluginabi.MethodHostModelExecute, hostModelExecutionRequest{
        HostModelExecutionRequest: pluginapi.HostModelExecutionRequest{
            EntryProtocol: opts.EntryProtocol, ExitProtocol: opts.ExitProtocol,
            Model: opts.Model, Stream: false, Body: body,
            Headers: cloneHeader(opts.Headers), Query: cloneValues(opts.Query), Alt: opts.Alt,
        },
        HostCallbackID: opts.HostCallbackID, // forwarded from management.handle's host_callback_id
    })
    var resp pluginapi.HostModelExecutionResponse
    json.Unmarshal(result, &resp)
    return resp, nil
}
```

Streaming variant returns `resp.StreamID`, then loops `host.model.stream_read` until
`chunk.Done`, with a `defer closeHostModelStream(resp.StreamID)` unless `implicit_close=true` was
requested (demo-only mode — normal code should always explicitly close).

The callback layer does **not** double-bill — `host.model.execute*` reuses the exact same
executor/usage-reporter path as a normal proxied request, so usage plugins still see one
`UsageRecord` per nested call, same as any other request.

### Credential file callback example

Resource query params `op=list|get|runtime|save`, `auth_index`, `name`, `json` map 1:1 to
`host.auth.list` / `host.auth.get` / `host.auth.get_runtime` / `host.auth.save`. (See the Gotcha
above — this example intentionally puts a privileged write behind an unauthenticated GET for
demonstration; do not copy that shape into production plugins.)

### Development notes / gotchas

- Always pair a `*_stream` open with the matching `*_stream_close` — the host holds server-side
  stream state keyed by `stream_id` until closed or the callback-context scope expires.
- Host callbacks run in-process with full trust — they are not a sandbox boundary. A plugin using
  `host.http.do`/`host.model.execute` is still fundamentally trusted code; these calls exist for
  convenience (shared proxy config, shared logging, shared billing/usage plumbing), not privilege
  separation.
- Never log credential JSON, tokens, or raw user request bodies through `host.log`.
- Prefer `host.model.*` over reimplementing your own outbound call with copied host credentials
  when you need to reuse the existing model execution path — you get proxy config, interceptor
  policy, and usage/billing accounting for free.
- Do not wire `host.auth.get`/`host.auth.save` (or any other privileged host callback) directly to
  unauthenticated resource-page query parameters for real functionality — route the privileged
  action through an authenticated `/v0/management/...` route instead (see §4's Trusted Resource
  Page Pattern).

### Vendored examples

`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/host-callback/go/main.go` — `host.log` +
`host.http.do` from a `management.handle` resource route.
`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/host-callback-auth-files/go/main.go` —
`host.auth.list`/`get`/`get_runtime`/`save` from resource query params (demo anti-pattern, see
above).
`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/host-model-callback/go/main.go` —
`host.model.execute`/`execute_stream` plus the `host_callback_id` recursion-guard forwarding
pattern in full.

All three declare `"capabilities":{"management_api":true}` since host callbacks aren't a capability
of their own — you reach them from inside whatever capability you did declare (most commonly
`management_api`'s `management.handle`, or `executor`).

---

## Cross-cutting mechanics every plugin author should know

1. **Transport**: all five capabilities ride the same native C-ABI plugin loaded via
   `buildmode=c-shared` (`.dylib`/`.so`/`.dll`); Go, Rust, and C examples exist side by side under
   `examples/<capability>/{go,rust,c}` in the full upstream repo (this vendored tree carries the Go
   variants). `pluginabi.ABIVersion = 1` is the C ABI shape version; `pluginabi.SchemaVersion = 1`
   is the RPC JSON contract version — new capabilities are added via new method names/capability
   flags, not by bumping `SchemaVersion`.
2. **Envelope**: every RPC response is `{"ok": bool, "result": <payload>, "error": {"code","message","retryable","http_status"}}`.
   Errors should set `error.code`/`error.message`; `retryable`/`http_status` are optional hints to
   the host.
3. **Plugin ID and priority**: a plugin's `id` is derived from its loaded file (basename), and its
   `priority` comes from `plugins.configs.<id>.priority` in host YAML. Records are sorted
   **descending** by priority, ties broken by ascending ID string. Scheduler and (implicitly)
   other single-winner capabilities use "first in this sorted order" as the effective precedence
   rule.
4. **Panic → fuse**: virtually every capability call site in `internal/pluginhost` wraps the plugin
   call in `defer recover()` that calls `h.fusePlugin(record.id, "<Capability.Method>", recovered)`
   on panic. A fused plugin is silently skipped for all future capability calls for the rest of the
   process's life — there is no automatic recovery, only a fresh load/reload. Defensive nil-checks
   and panic-free error returns in plugin code are load-bearing, not just good style.
5. **Config delivery**: plugin-specific config lives under `plugins.configs.<id>` in host YAML,
   delivered to the plugin as raw `config_yaml` bytes inside `plugin.register`/`plugin.reconfigure`;
   only `enabled`/`priority` are host-reserved keys, everything else is plugin-defined and declared
   for UI purposes via `Metadata.ConfigFields` (`ConfigFieldTypeBoolean/String/Integer/Enum`).
6. **`examples/simple/go/main.go`** is the closest thing to a canonical schema reference — it
   turns on most capability flags at once and shows most method names from `pluginabi` in one
   `switch`, but it does **not** implement `scheduler.pick`, `model.route`, or any of the three
   interceptor methods (`request.intercept_before`/`_after`, `response.intercept_after`,
   `response.intercept_stream_chunk`) — those five have no handler case in that file at all. Use it
   for the RPC shapes it does cover, then use each capability's own dedicated example (this file's
   tables) for realistic single-purpose plugins.
