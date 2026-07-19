# Translators, Normalizers, Interceptors, Thinking Applier — depth reference

Covers: `request_translator`, `response_translator`, `request_normalizer`,
`response_before_translator`, `response_after_translator`, `request_interceptor`,
`response_interceptor`, `response_stream_interceptor`, `thinking_applier`.

Ground truth: `${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginapi-types.go` (all
request/response structs and interfaces), `${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginabi-types.go`
(the RPC method-name constants). Per-capability docs under
`${CLAUDE_PLUGIN_ROOT}/references/upstream/docs-plugin/`. Pinned to upstream **v7.2.88**.

---

## 0. The big picture: where these capabilities sit in the request pipeline

```
client body (client protocol, e.g. chat-completions)
   │
   ▼
[request_normalizer]              request.normalize        — canonicalize/fix payload, same-or-different format annotated by FromFormat/ToFormat
   │
   ▼
[request_interceptor before-auth] request.intercept_before  — rewrite headers/body; ToFormat may still be empty (no upstream/credential chosen yet)
   │
   ▼           (credential / auth / upstream provider selection happens here, host-owned)
   ▼
[request_interceptor after-auth]  request.intercept_after   — rewrite headers/body; Model + ToFormat now concrete
   │
   ▼
[request_translator]              request.translate         — canonical → upstream provider payload
   │
   ▼         (upstream HTTP call executed by the executor/scheduler — NOT by any of these capabilities)
   ▼
[response_before_translator]      response.normalize_before — fix up the raw provider-native response
   │
   ▼
[response_translator]             response.translate        — provider payload → client/canonical protocol
   │
   ▼
[response_after_translator]       response.normalize_after  — final polish of the already-translated client response
   │
   ▼
[response_interceptor]            response.intercept_after  — non-streaming only: last chance to rewrite headers/body
   or
[response_stream_interceptor]     response.intercept_stream_chunk — streaming only: per-SSE-chunk rewrite
   │
   ▼
client response
```

`thinking_applier` is orthogonal to this pipeline: it is invoked by the host's own thinking
subsystem (`internal/thinking/`) whenever a canonical thinking configuration needs to be written
into a provider-specific payload field, for providers the plugin declares via
`thinking.identifier`. It is not wired into the translator/normalizer/interceptor chain directly.

---

## 1. The crisp distinction: Translator vs Normalizer vs Interceptor

This is the single most important conceptual thing for a plugin author to get right, because the
three families look superficially similar (all take a `Body []byte`, all return a rewritten
`Body []byte`) but have very different host-side selection/execution semantics and very different
intended jobs.

| | **Translator** (`request_translator`, `response_translator`) | **Normalizer** (`request_normalizer`, `response_before_translator`, `response_after_translator`) | **Interceptor** (`request_interceptor`, `response_interceptor`, `response_stream_interceptor`) |
|---|---|---|---|
| **Job** | Convert between two *different* wire protocols/formats (e.g. `chat-completions` → `anthropic`, or `codex` → `chat-completions`). Format-changing. | Rewrite a payload that stays in the *same* protocol shape — fill defaults, fix a provider's non-standard fields, lightweight rewrite. Format-preserving. | Rewrite **headers and/or body** around the edges of execution — before credential selection, after credential selection, or on the final HTTP response/stream. Not concerned with protocol translation at all; can touch `http.Header`. |
| **Selection when multiple plugins declare it** | **First match wins.** The host walks active plugins in priority order and calls each declared translator; the **first one that returns a non-empty `Body`** short-circuits the loop and its output is used. Other translators are never called for that request. | **All of them run, chained.** The host walks active plugins in priority order and feeds each plugin's output as the next plugin's input (`current = normalized`). Every declared normalizer that returns a non-empty body gets to touch the payload. | **All of them run, chained.** Same chaining pattern as normalizers, but the accumulator carries *both* headers and body: `Headers` are merged (`mergeHeaders`, later plugins' matching header names win, `ClearHeaders` deletes first), and `Body` is replaced whenever a plugin returns a non-empty one. |
| **Can it fail silently / partially?** | Yes by design — if it returns an error or empty `Body`, the host just tries the next candidate (for request/response translate) or falls back to the original body untranslated (`TranslateRequest`/`TranslateResponse` return `(body, false)` if nothing matched). | Yes — an empty `Body`/error from one normalizer just means that stage is skipped; the previous `current` value passes through unchanged to the next normalizer in the chain. | Yes — an error or non-OK call from one interceptor just means the host keeps `current` as it stood before calling that plugin, then moves to the next plugin in the chain. |
| **Touches HTTP headers?** | No — only `Body`. | No — only `Body`. | **Yes** — this is the *only* capability family with `Headers`/`ClearHeaders` in its response shape. If you need to change a response header (not the body), use `response_interceptor` / `response_stream_interceptor`, not a normalizer. The `response-after-translator.md` doc says this explicitly: "To change HTTP headers, use the response interceptor capability, not response normalization." |
| **Runs relative to upstream HTTP call** | Before (`request_translator`) or after (`response_translator`) the upstream call, but *never* performs the call itself. | Before (`request_normalizer`) or after (`response_before/after_translator`) the upstream call; never performs the call. | Around the upstream call: `request_interceptor` before/after credential selection (both pre-execution), `response_interceptor`/`response_stream_interceptor` strictly post-execution on the response path. |
| **Streaming** | `response_translator` streaming support is host/format-dependent — the doc explicitly says to test it. | Same caveat implicitly (bodies are whole-payload transforms). | `response_interceptor` is **non-streaming only**; streaming responses go through the *separate* `response_stream_interceptor` capability, which operates per-chunk instead of on a whole body. |

In one sentence each:

- **Translator = "I speak protocol A, the other side speaks protocol B, let me convert."**
- **Normalizer = "The payload is already in the right protocol shape, but it's slightly
  broken/incomplete — let me patch it."**
- **Interceptor = "I want to observe/mutate the request or response envelope (headers + body) at a
  specific point in the execution lifecycle," including points that have nothing to do with
  cross-protocol translation** (e.g. rewriting headers before credentials are even picked).

---

## 2. Shared wire mechanics

All of these capabilities are declared through the same `Capabilities` struct
(`sdk/pluginapi/types.go`, `Capabilities` type). Each capability field is a Go interface; a plugin
only needs to implement the interfaces for the capabilities it wants, and the corresponding
boolean flag in the JSON `capabilities` object (used over the C-ABI/RPC wire, see
`examples/simple/go/main.go`'s `registrationCapability` struct) tells the host it's present.

```go
// sdk/pluginapi/types.go — Capabilities struct (relevant fields)
type Capabilities struct {
    ...
    RequestTranslator        RequestTranslator
    RequestNormalizer        RequestNormalizer
    ResponseTranslator       ResponseTranslator
    ResponseBeforeTranslator ResponseNormalizer   // NOTE: same Go interface type as ResponseAfterTranslator
    ResponseAfterTranslator  ResponseNormalizer   // both response_before_translator and response_after_translator use ResponseNormalizer
    RequestInterceptor       RequestInterceptor
    ResponseInterceptor      ResponseInterceptor
    StreamChunkInterceptor   StreamChunkInterceptor
    ThinkingApplier          ThinkingApplier
    ...
}
```

Note the reuse: `response_before_translator` and `response_after_translator` are both backed by the
*same* Go interface, `ResponseNormalizer` (single method `NormalizeResponse`). The host
distinguishes which stage it's calling by which struct field it invokes
(`Capabilities.ResponseBeforeTranslator` vs `Capabilities.ResponseAfterTranslator`) — a plugin can
implement one Go type and assign it to both fields if it wants to run at both stages. The vendored
`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/response-normalizer/go/main.go` does exactly
that:

```json
{"capabilities":{"response_before_translator":true,"response_after_translator":true}}
```

and dispatches on the RPC `method` string `response.normalize_before` vs `response.normalize_after`
inside its own `handleMethod` switch.

RPC method-name constants (`sdk/pluginabi/types.go`) — this is the exact string that arrives over
the plugin RPC/ABI boundary and that a hand-rolled `handleMethod(method string, ...)` switch must
match on:

```go
const (
    MethodRequestTranslate       = "request.translate"
    MethodRequestNormalize       = "request.normalize"
    MethodRequestInterceptBefore = "request.intercept_before"
    MethodRequestInterceptAfter  = "request.intercept_after"

    MethodResponseTranslate            = "response.translate"
    MethodResponseNormalizeBefore      = "response.normalize_before"
    MethodResponseNormalizeAfter       = "response.normalize_after"
    MethodResponseInterceptAfter       = "response.intercept_after"
    MethodResponseInterceptStreamChunk = "response.intercept_stream_chunk"

    MethodThinkingIdentifier = "thinking.identifier"
    MethodThinkingApply      = "thinking.apply"
)
```

### Ordering / priority — how it actually works (`internal/pluginhost/snapshot.go`)

Plugin instances are configured per-ID under `plugins.configs.<pluginID>` in the host YAML config
(`internal/config/config.go`, `PluginInstanceConfig`):

```go
type PluginInstanceConfig struct {
    Enabled  *bool     `yaml:"enabled,omitempty" json:"enabled,omitempty"`
    Priority int       `yaml:"priority,omitempty" json:"priority,omitempty"` // controls plugin startup and routing order
    Raw      yaml.Node `yaml:"-" json:"-"`
}
```

```yaml
plugins:
  configs:
    codex-service-tier:
      enabled: true
      priority: 1
```

Default `Priority` is `0`. All active plugin `capabilityRecord`s are sorted once via
`sortRecords` (`internal/pluginhost/snapshot.go`):

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

**Higher `priority` number sorts first.** Ties are broken by plugin ID, ascending
(alphabetically-earlier plugin ID runs first). `h.activeRecords()` returns this sorted list, and
every dispatch loop shown below (`NormalizeRequest`, `TranslateRequest`, `interceptRequest`,
`InterceptResponse`, `InterceptStreamChunk`, etc.) simply `range`s over `h.activeRecords()`, so
**this same priority-desc/id-asc order governs every capability family** — it's just that
translators stop at the first success while normalizers/interceptors run the whole chain (see §1
table).

---

## 3. Request Translator (`request_translator`)

**Purpose:** convert a canonical (host-normalized) request body into the target upstream
provider's wire payload. Runs in the protocol-translation stage, right before request execution.

### Capability flag
```json
{ "capabilities": { "request_translator": true } }
```

### Go interface (`sdk/pluginapi/types.go`)
```go
type RequestTranslator interface {
    TranslateRequest(context.Context, RequestTransformRequest) (PayloadResponse, error)
}

type RequestTransformRequest struct {
    FromFormat string // source protocol format
    ToFormat   string // target protocol format
    Model      string // requested model identifier
    Stream     bool
    Body       []byte
}

type PayloadResponse struct {
    Body []byte
}
```

### RPC method
| Method | Purpose |
|---|---|
| `request.translate` | Converts `Body` from `FromFormat` to `ToFormat`. |

### Wire example
Request:
```json
{
  "FromFormat": "chat-completions",
  "ToFormat": "anthropic",
  "Model": "claude-sonnet",
  "Stream": false,
  "Body": "base64-request-body"
}
```
Response:
```json
{ "Body": "base64-translated-body" }
```

### Host dispatch (`internal/pluginhost/adapters.go`)
```go
func (h *Host) TranslateRequest(ctx context.Context, from, to sdktranslator.Format, model string, body []byte, stream bool) ([]byte, bool) {
    for _, record := range h.activeRecords() {
        if h.isPluginFused(record.id) || record.plugin.Capabilities.RequestTranslator == nil {
            continue
        }
        if translated, ok := h.callRequestTranslator(ctx, record, from, to, model, body, stream); ok {
            return translated, true
        }
    }
    return bytes.Clone(body), false
}
```
`callRequestTranslator` calls `TranslateRequest`, and treats an error **or an empty `Body`** as "I
don't handle this" (`ok=false`) — the loop moves on to the next plugin. If nothing matches, the
original body is returned unchanged and `ok=false` bubbles up to the caller.

### Difference from Request Normalizer
- `request_normalizer` normalizes provider- or special-entry requests into the canonical format the
  host understands (format-preserving cleanup).
- `request_translator` converts *from* the canonical format *into* the target upstream protocol
  (format-changing).

### Development notes
- Only handle format combinations you explicitly support. Return an error, or simply don't declare
  the capability, for combinations you can't handle — since an empty `Body`/error is the "not
  handled" signal that lets another plugin (or the host's own native translator) take over.
- `Body` must be a **complete and valid** target-protocol payload.
- **Do not** do credential selection or make upstream HTTP requests inside a translator — that's
  the scheduler/executor's job.

### Vendored example
`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/request-translator/go/main.go` — the dedicated
single-purpose example. Minimal cgo/C-ABI skeleton; the interesting bit is the `request.translate`
case, which returns a fixed base64 body:
```go
case "request.translate":
    return okEnvelopeJSON("{\"Body\":\"eyJ0cmFuc2xhdGVkX2J5IjoiZXhhbXBsZS1yZXF1ZXN0LXRyYW5zbGF0b3ItZ28ifQ==\"}")
```
A real translator would `json.Unmarshal(raw, &pluginapi.RequestTransformRequest{})`, base64-decode
`.Body`, rewrite the JSON into the target shape, and return
`okEnvelope(pluginapi.PayloadResponse{Body: <bytes>})`.

---

## 4. Response Translator (`response_translator`)

**Purpose:** the symmetric counterpart of the request translator — converts a canonical response
back into the client-requested protocol. Runs after the upstream response returns, before it's
sent to the client.

### Capability flag
```json
{ "capabilities": { "response_translator": true } }
```

### Go interface
```go
type ResponseTranslator interface {
    TranslateResponse(context.Context, ResponseTransformRequest) (PayloadResponse, error)
}

type ResponseTransformRequest struct {
    FromFormat        string
    ToFormat          string
    Model             string
    Stream            bool
    OriginalRequest   []byte // raw client request body
    TranslatedRequest []byte // request body actually sent upstream
    Body              []byte // response payload to transform
}
```

### RPC method
| Method | Purpose |
|---|---|
| `response.translate` | Converts response `Body` from `FromFormat` to `ToFormat`. |

### Wire example
```json
{
  "FromFormat": "codex",
  "ToFormat": "chat-completions",
  "Model": "gpt-5.5",
  "Stream": false,
  "OriginalRequest": "base64-client-body",
  "TranslatedRequest": "base64-provider-request",
  "Body": "base64-upstream-response"
}
```
```json
{ "Body": "base64-client-response" }
```

### Host dispatch — same first-match-wins pattern as request translation
```go
func (h *Host) TranslateResponse(ctx context.Context, from, to sdktranslator.Format, model string,
    originalRequestRawJSON, requestRawJSON, body []byte, stream bool) ([]byte, bool) {
    for _, record := range h.activeRecords() {
        translator := record.plugin.Capabilities.ResponseTranslator
        if h.isPluginFused(record.id) || translator == nil {
            continue
        }
        if translated, ok := h.callResponseTranslator(ctx, record, translator, from, to, model, originalRequestRawJSON, requestRawJSON, body, stream); ok {
            return translated, true
        }
    }
    return bytes.Clone(body), false
}
```

### Development notes
- `OriginalRequest` (raw client body) and `TranslatedRequest` (what was actually sent upstream) are
  both provided so a translator can cross-reference request context (e.g. to know which tools were
  requested, or what `tool_choice` was, when constructing a client-shaped response).
- Output must be a **complete** response required by the client protocol.
- Streaming support for response translation is host/format dependent — the doc explicitly warns:
  "Plugins should explicitly test streaming scenarios."

### Vendored example
`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/response-translator/go/main.go` — the dedicated
single-purpose example (trivial echo, same shape as request-translator's stub).

---

## 5. Request Normalizer (`request_normalizer`)

**Purpose:** rewrite a request payload *before it enters the execution path*, into a shape later
host stages handle more easily — filling defaults, fixing provider-specific quirks, lightweight
rewrites. Explicitly **not** a translator: it doesn't need to change protocol format (though
`FromFormat`/`ToFormat` are still passed for context).

### Capability flag
```json
{ "capabilities": { "request_normalizer": true } }
```

### Go interface
```go
type RequestNormalizer interface {
    NormalizeRequest(context.Context, RequestTransformRequest) (PayloadResponse, error)
}
```
Uses the *same* `RequestTransformRequest`/`PayloadResponse` types as the request translator.

### RPC method
| Method | Purpose |
|---|---|
| `request.normalize` | Returns a new request body based on format, model, and stream flag. |

### Host dispatch — chained, not first-match
```go
func (h *Host) NormalizeRequest(ctx context.Context, from, to sdktranslator.Format, model string, body []byte, stream bool) []byte {
    current := bytes.Clone(body)
    for _, record := range h.activeRecords() {
        if h.isPluginFused(record.id) || record.plugin.Capabilities.RequestNormalizer == nil {
            continue
        }
        if normalized, ok := h.callRequestNormalizer(ctx, record, from, to, model, current, stream); ok {
            current = normalized
        }
    }
    return current
}
```
Every declared normalizer runs, in priority order, each one seeing the *previous* normalizer's
output as its input (`current`). A normalizer that returns an empty `Body` or an error is skipped
(`current` unchanged) — this is the explicit documented mechanism for "keep the original content":
> "An empty `Body` prevents the host from applying an effective rewrite. Return the original `Body`
> when the original content should be kept."

### Real, worked example: `codex-service-tier`
This is the doc's own pointer to "closer to real usage." It reads a `fast` boolean from
plugin-owned YAML config (`plugins.configs.codex-service-tier.fast: true`) and, only when
`ToFormat == "codex"` **and** `Model == "gpt-5.5"` **and** `fast` is enabled, sets
`service_tier: "priority"` on the outgoing Codex request body via `sjson`:

```go
func shouldSetPriorityServiceTier(req pluginapi.RequestTransformRequest) bool {
    if !fastEnabled.Load() {
        return false
    }
    if !strings.EqualFold(req.ToFormat, "codex") {
        return false
    }
    return req.Model == "gpt-5.5"
}

func setPriorityServiceTier(body []byte) ([]byte, bool) {
    updated, errSet := sjson.SetBytes(body, "service_tier", "priority")
    if errSet != nil {
        return nil, false
    }
    return updated, true
}
```

Config-loading pattern (applies to **every** capability, not just normalizers — this is the
canonical way a plugin picks up its own YAML config):
```go
type lifecycleRequest struct {
    ConfigYAML []byte `json:"config_yaml"`
}
type pluginConfig struct {
    Fast bool `yaml:"fast"`
}

func configure(raw []byte) error {
    var req lifecycleRequest
    json.Unmarshal(raw, &req)
    cfg := pluginConfig{}
    if len(req.ConfigYAML) > 0 {
        yaml.Unmarshal(req.ConfigYAML, &cfg)
    }
    fastEnabled.Store(cfg.Fast)
    return nil
}
```
`config_yaml` is delivered on **both** `plugin.register` and `plugin.reconfigure` — parse and cache
it in both handlers (the codex-service-tier example routes both methods to the same `configure`
call).

Config surface declared for management clients:
```go
ConfigFields: []pluginapi.ConfigField{{
    Name:        "fast",
    Type:        pluginapi.ConfigFieldTypeBoolean,
    Description: "Sets Codex gpt-5.5 Responses requests to the priority service tier.",
}},
```
matching config:
```yaml
plugins:
  configs:
    codex-service-tier:
      enabled: true
      priority: 1
      fast: true
```

### Development notes
- Keep it narrow and predictable — don't take on executor responsibilities (no upstream calls, no
  credential logic).
- Plugin-owned config arrives via `config_yaml` on both `plugin.register` and `plugin.reconfigure` —
  parse/cache it in both.

### Vendored examples
`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/codex-service-tier/go/main.go` — the real,
worked example above (this is what to study for actual production logic). Also
`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/request-normalizer/go/main.go` — the dedicated
single-purpose skeleton example (trivial echo).

---

## 6. Response Pre-Translation Normalizer (`response_before_translator`)

**Purpose:** rewrite the **upstream provider-native** response *before* the host's own response
translation runs. Good for patching missing/non-standard provider fields prior to any translation
step (host-native or plugin `response_translator`).

### Capability flag
```json
{ "capabilities": { "response_before_translator": true } }
```

### Go interface
Backed by the shared `ResponseNormalizer` interface:
```go
type ResponseNormalizer interface {
    NormalizeResponse(context.Context, ResponseTransformRequest) (PayloadResponse, error)
}
```

### RPC method
| Method | Purpose |
|---|---|
| `response.normalize_before` | Returns the normalized response body **before** response translation. |

### Host dispatch — chained
```go
func (h *Host) NormalizeResponseBefore(ctx context.Context, from, to sdktranslator.Format, model string,
    originalRequestRawJSON, requestRawJSON, body []byte, stream bool) []byte {
    current := bytes.Clone(body)
    for _, record := range h.activeRecords() {
        normalizer := record.plugin.Capabilities.ResponseBeforeTranslator
        if h.isPluginFused(record.id) || normalizer == nil {
            continue
        }
        if normalized, ok := h.callResponseNormalizer(ctx, record, "ResponseBeforeTranslator.NormalizeResponse", normalizer, from, to, model, originalRequestRawJSON, requestRawJSON, current, stream); ok {
            current = normalized
        }
    }
    return current
}
```

### Development notes
- Suitable for fixing missing upstream fields or non-standard provider payload shapes.
- **Do not** output the client protocol format here unless the current stage's `ToFormat` already
  *is* the client format (i.e. don't jump ahead and pre-translate — that's the translator's job).
- A single plugin can implement both `response_before_translator` and `response_after_translator`
  (they share the `ResponseNormalizer` Go interface — see the vendored `response-normalizer`
  example below, which registers both flags and dispatches on the RPC method string).

### Vendored example
`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/response-normalizer/go/main.go` — the dedicated
example that implements **both** `response_before_translator` and `response_after_translator` in
one plugin.

---

## 7. Response Post-Translation Normalizer (`response_after_translator`)

**Purpose:** final rewrite pass **after** the response has already been translated into the
client's protocol. Good for strict-client compatibility shims, filling required-but-missing
fields, lightweight post-processing.

### Capability flag
```json
{ "capabilities": { "response_after_translator": true } }
```

### Go interface
Same shared `ResponseNormalizer` interface as §6.

### RPC method
| Method | Purpose |
|---|---|
| `response.normalize_after` | Returns the normalized **client** response body after response translation. |

### Wire example
```json
{
  "FromFormat": "codex",
  "ToFormat": "chat-completions",
  "Model": "gpt-5.5",
  "Stream": false,
  "OriginalRequest": "base64-client-body",
  "TranslatedRequest": "base64-provider-request",
  "Body": "base64-translated-response"
}
```
```json
{ "Body": "base64-final-client-response" }
```

### Host dispatch — chained, mirrors `NormalizeResponseBefore`
```go
func (h *Host) NormalizeResponseAfter(ctx context.Context, from, to sdktranslator.Format, model string,
    originalRequestRawJSON, requestRawJSON, body []byte, stream bool) []byte {
    current := bytes.Clone(body)
    for _, record := range h.activeRecords() {
        normalizer := record.plugin.Capabilities.ResponseAfterTranslator
        ...
        if normalized, ok := h.callResponseNormalizer(ctx, record, "ResponseAfterTranslator.NormalizeResponse", normalizer, from, to, model, originalRequestRawJSON, requestRawJSON, current, stream); ok {
            current = normalized
        }
    }
    return current
}
```

### Development notes
- Good for filling client-protocol-required compatibility fields.
- **Do not** call upstream again here, and do not change billing semantics.
- To change HTTP response **headers** (not body), use `response_interceptor` instead — normalizers
  never see or touch `http.Header`.

### Vendored example
`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/response-normalizer/go/main.go` — same plugin
as §6, dispatched by RPC method:
```go
"capabilities":{"response_before_translator":true,"response_after_translator":true}
...
case "response.normalize_before":
    return okEnvelopeJSON("{\"Body\":\"...response_normalized_before_by...\"}")
case "response.normalize_after":
    return okEnvelopeJSON("{\"Body\":\"...response_normalized_after_by...\"}")
```

---

## 8. Request Interceptor (`request_interceptor`)

**Purpose:** rewrite request **headers or body** before the upstream request executes. Unlike
translators/normalizers, this operates on the full request envelope (`http.Header` + `Body`), and
has **two distinct call sites** in the pipeline — this is the mechanism to use for anything that
isn't strictly a protocol-format concern (auth header injection, feature-flag headers, request
logging/mutation tied to credential context, etc.).

### Capability flag
```json
{ "capabilities": { "request_interceptor": true } }
```

### Go interface
```go
type RequestInterceptor interface {
    InterceptRequestBeforeAuth(context.Context, RequestInterceptRequest) (RequestInterceptResponse, error)
    InterceptRequestAfterAuth(context.Context, RequestInterceptRequest) (RequestInterceptResponse, error)
}

type RequestInterceptRequest struct {
    SourceFormat   string          // original client protocol format
    ToFormat       string          // selected upstream protocol format; EMPTY before credential selection
    Model          string          // current execution model; after auth-selection this is the upstream model
    RequestedModel string          // client-requested model, before alias/model-pool rewriting
    Stream         bool
    Headers        http.Header     // current upstream request headers
    Body           []byte
    Metadata       map[string]any  // best-effort cloned context snapshot — read-only
}

type RequestInterceptResponse struct {
    Headers      http.Header // overrides headers with same name; preserves unmentioned headers
    Body         []byte      // replaces current body only when non-empty
    ClearHeaders []string    // removes named headers BEFORE Headers is applied
}
```

### RPC methods — two stages, two methods
| Method | Purpose |
|---|---|
| `request.intercept_before` | Rewrites the request **before** credential selection. `ToFormat` may be empty at this point. |
| `request.intercept_after` | Rewrites the request **after** credential selection. `Model` and `ToFormat` are concrete/specific here. |

A single plugin implements both methods on the same `RequestInterceptor` Go interface; the host
calls `InterceptRequestBeforeAuth` at one point in the pipeline and `InterceptRequestAfterAuth` at
another.

### Host dispatch — chained across headers AND body
```go
func (h *Host) interceptRequest(ctx context.Context, req pluginapi.RequestInterceptRequest, method string,
    invoke func(...) (...), skipPluginID string) pluginapi.RequestInterceptResponse {
    current := pluginapi.RequestInterceptResponse{
        Headers: cloneHeader(req.Headers),
        Body:    bytes.Clone(req.Body),
    }
    for _, record := range h.activeRecords() {
        interceptor := record.plugin.Capabilities.RequestInterceptor
        if h.isPluginFused(record.id) || interceptor == nil || record.id == skipPluginID {
            continue
        }
        nextReq := req
        nextReq.Headers  = cloneHeader(current.Headers)
        nextReq.Body     = bytes.Clone(current.Body)
        nextReq.Metadata = cloneInterceptorMetadata(req.Metadata)
        if resp, ok := h.callRequestInterceptor(ctx, record, method, ..., nextReq); ok {
            current.Headers = mergeHeaders(current.Headers, resp.Headers, resp.ClearHeaders)
            if len(resp.Body) > 0 {
                current.Body = bytes.Clone(resp.Body)
            }
        }
    }
    return current
}
```
Each plugin in the chain sees the *accumulated* result of every prior plugin (both headers and
body), in priority order. `ClearHeaders` is applied before `Headers` on each step. An empty
returned `Body` means "I didn't change the body" (previous value is kept) — same convention as
normalizers.

### Recursion guard
When a plugin starts a nested model request through `host.model.*` and passes a
`host_callback_id`, the host **skips that originating plugin's own request interceptors** for the
nested call (`InterceptRequestBeforeAuthExcept` / `InterceptRequestAfterAuthExcept` take a
`skipPluginID`) — this prevents infinite self-recursion. Interceptors belonging to *other* plugins
still run on the nested request.

### Development notes
- `Metadata` is a best-effort cloned, read-only context snapshot — don't mutate expecting it to
  flow anywhere; it's JSON-like data for inspection only.
- Don't rely on credential fields **before** credential selection (`intercept_before`); do
  credential-context-dependent rewrites in `intercept_after` instead.
- **Never** call an upstream model directly from inside a request interceptor — use host callbacks
  (`host.model.*`, see `${CLAUDE_PLUGIN_ROOT}/skills/cpa-capabilities/references/ops-scheduler-usage-cli-mgmt.md`)
  if you truly need to issue a nested model request from within this hook.

### Vendored example
**None dedicated.** Implement directly from `RequestInterceptor` in
`${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginapi-types.go` — there is no
`examples/request-interceptor/go` in this vendored tree, and `examples/simple/go/main.go` does not
implement `request.intercept_before`/`request.intercept_after` either. The upstream doc itself
points at `internal/pluginhost/adapters_test.go` (host-side tests, not a plugin example) and an
`examples/plugin/antigravity-web-search/go/main.go` migration example that is **not** part of this
vendored `examples/` tree — treat that citation as informational only, not something to open here.

---

## 9. Response Interceptor (`response_interceptor`)

**Purpose:** rewrite response headers/body for a **successful, non-streaming** HTTP execution
response, right before it's returned to the client.

### Capability flag
```json
{ "capabilities": { "response_interceptor": true } }
```

### Go interface
```go
type ResponseInterceptor interface {
    InterceptResponse(context.Context, ResponseInterceptRequest) (ResponseInterceptResponse, error)
}

type ResponseInterceptRequest struct {
    SourceFormat    string
    Model           string
    RequestedModel  string
    Stream          bool
    RequestHeaders  http.Header
    ResponseHeaders http.Header
    OriginalRequest []byte // raw client request body
    RequestBody     []byte // upstream request body
    Body            []byte // response body
    StatusCode      int
    Metadata        map[string]any
}

type ResponseInterceptResponse struct {
    Headers      http.Header
    Body         []byte
    ClearHeaders []string
}
```

### RPC method
| Method | Purpose |
|---|---|
| `response.intercept_after` | Rewrites successful non-streaming responses. |

### Host dispatch — chained (identical accumulation pattern to request interceptor)
```go
func (h *Host) InterceptResponseExcept(ctx context.Context, req pluginapi.ResponseInterceptRequest, skipPluginID string) pluginapi.ResponseInterceptResponse {
    current := pluginapi.ResponseInterceptResponse{
        Headers: cloneHeader(req.ResponseHeaders),
        Body:    bytes.Clone(req.Body),
    }
    for _, record := range h.activeRecords() {
        interceptor := record.plugin.Capabilities.ResponseInterceptor
        if h.isPluginFused(record.id) || interceptor == nil || record.id == skipPluginID {
            continue
        }
        ...
        if resp, ok := h.callResponseInterceptor(ctx, record, interceptor, nextReq); ok {
            current.Headers = mergeHeaders(current.Headers, resp.Headers, resp.ClearHeaders)
            if len(resp.Body) > 0 { current.Body = bytes.Clone(resp.Body) }
        }
    }
    return current
}
```

### Development notes
- **Non-streaming only.** For streaming responses, use `response_stream_interceptor` (§10) instead
  — this capability is simply never invoked on a streaming response.
- `Headers` overrides same-name response headers, preserving all unmentioned ones; a non-empty
  `Body` fully replaces the response body.
- Same recursion guard as request interceptors: a nested `host.model.*` call started via
  `host_callback_id` skips the *originating* plugin's own response interceptors.

### Vendored example
**None dedicated.** Implement directly from `ResponseInterceptor` in
`${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginapi-types.go`. Same caveat as §8: the upstream
doc's own citations here (`adapters_test.go`, `antigravity-web-search`) are host-side tests / an
example not present in this vendored tree.

---

## 10. Streaming Response Interceptor (`response_stream_interceptor`)

**Purpose:** rewrite or drop individual SSE/streaming response chunks before they reach the
client, and adjust stream response headers at initialization. This is the streaming-shaped
sibling of `response_interceptor`.

### Capability flag
```json
{ "capabilities": { "response_stream_interceptor": true } }
```

### Go interface
```go
type StreamChunkInterceptor interface {
    InterceptStreamChunk(context.Context, StreamChunkInterceptRequest) (StreamChunkInterceptResponse, error)
}

type StreamChunkInterceptRequest struct {
    SourceFormat    string
    Model           string
    RequestedModel  string
    RequestHeaders  http.Header
    ResponseHeaders http.Header
    OriginalRequest []byte
    RequestBody     []byte
    Body            []byte     // current chunk payload
    HistoryChunks   [][]byte   // bounded recent-chunk history: up to 64 chunks / 1 MiB total
    ChunkIndex      int        // 0-based; StreamChunkHeaderInitIndex (-1) = header-only init call
    Metadata        map[string]any
}

type StreamChunkInterceptResponse struct {
    Headers      http.Header
    Body         []byte
    ClearHeaders []string
    DropChunk    bool // skip this payload chunk; still keeps header changes
}

const StreamChunkHeaderInitIndex = -1
```

### RPC method
| Method | Purpose |
|---|---|
| `response.intercept_stream_chunk` | Rewrites streaming response header initialization, or a single payload chunk. |

### `ChunkIndex` semantics
- `ChunkIndex == -1` (`StreamChunkHeaderInitIndex`): a **header-only initialization call** — no
  payload chunk yet, this is your one chance to adjust response headers before any chunk is
  streamed.
- `ChunkIndex >= 0`: a real payload chunk, 0-indexed.

### Host dispatch — chained, with early-exit on drop
```go
func (h *Host) InterceptStreamChunkExcept(ctx context.Context, req pluginapi.StreamChunkInterceptRequest, skipPluginID string) pluginapi.StreamChunkInterceptResponse {
    current := pluginapi.StreamChunkInterceptResponse{
        Headers: cloneHeader(req.ResponseHeaders),
        Body:    bytes.Clone(req.Body),
    }
    for _, record := range h.activeRecords() {
        interceptor := record.plugin.Capabilities.StreamChunkInterceptor
        if h.isPluginFused(record.id) || interceptor == nil || current.DropChunk || record.id == skipPluginID {
            continue // once DropChunk is true, remaining interceptors in the chain are skipped for this chunk
        }
        ...
        if resp, ok := h.callStreamChunkInterceptor(ctx, record, interceptor, nextReq); ok {
            current.Headers = mergeHeaders(current.Headers, resp.Headers, resp.ClearHeaders)
            if len(resp.Body) > 0 { current.Body = bytes.Clone(resp.Body) }
            if resp.DropChunk { current.DropChunk = true /* header changes still apply */ }
        }
    }
    return current
}
```
Once any interceptor in the chain sets `DropChunk: true`, the loop condition
`current.DropChunk` causes subsequent plugins in the *same chunk's* chain to be skipped — but
header changes already accumulated are preserved and still applied.

### History window
`HistoryChunks` retains a **bounded** snapshot of recently-delivered chunks: **up to 64 chunks and
1 MiB of history bytes total**. A dropped chunk (`DropChunk: true`) never enters this history. Do
not assume `HistoryChunks` contains the entire stream — it's a rolling window, not a full replay
buffer.

### Development notes
- **Do not make high-latency external calls per chunk** — this runs on the hot streaming path,
  once per SSE chunk.
- **Preserve SSE protocol boundaries** — don't corrupt `data:` lines, blank-line frame separators,
  or terminal chunks (`data: [DONE]`-style markers) when rewriting `Body`.
- Same recursion guard: nested `host.model.*` calls with `host_callback_id` skip the originating
  plugin's own streaming interceptors.

### Vendored example
**None dedicated.** Implement directly from `StreamChunkInterceptor` in
`${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginapi-types.go`. Upstream's own doc citations here
(`adapters_test.go`) are host-side tests, not a plugin to copy from.

---

## 11. Thinking Applier (`thinking_applier`)

**Purpose:** the *last* mile of a "canonical thinking config → provider-specific payload fields"
pipeline the host owns end-to-end up until this point. The host (`internal/thinking/`) already
parses, normalizes, and validates the client's thinking/reasoning request (suffixes like
`-thinking-high`, budget numbers, etc.) into a canonical `ThinkingConfig` before ever calling a
plugin. This capability exists **only** so a plugin-defined provider can receive that canonical
config and write it into its own payload shape — it explicitly preserves the architecture
boundary "canonical thinking config → provider fields," i.e. plugins never see or reimplement the
parsing/validation logic themselves.

### Capability flag
```json
{ "capabilities": { "thinking_applier": true } }
```

### Go interface
```go
type ThinkingApplier interface {
    Identifier() string // provider key handled by this applier
    ApplyThinking(context.Context, ThinkingApplyRequest) (PayloadResponse, error)
}

type ThinkingConfig struct {
    Mode   string // canonical mode: "budget", "level", "none", or "auto"
    Budget int    // normalized thinking token budget
    Level  string // normalized named effort level
}

type ThinkingApplyRequest struct {
    Provider string    // normalized provider key being applied
    Model    ModelInfo // model metadata
    Config   ThinkingConfig
    Body     []byte    // provider payload to rewrite
}
```

### RPC methods
| Method | Purpose |
|---|---|
| `thinking.identifier` | Returns the provider identifier handled by this plugin. |
| `thinking.apply` | Applies the canonical thinking configuration to a provider payload. |

### Wire example
```json
{
  "Provider": "plugin-example",
  "Model": {
    "ID": "plugin-example-model",
    "Thinking": { "Min": 0, "Max": 32768, "ZeroAllowed": true, "DynamicAllowed": true, "Levels": ["low","medium","high"] }
  },
  "Config": { "Mode": "budget", "Budget": 1024, "Level": "" },
  "Body": "base64-provider-payload"
}
```
```json
{ "Body": "base64-provider-payload-with-thinking" }
```
`Config` arrives **already parsed and normalized** — the plugin does not re-parse suffixes or raw
thinking input from the client request.

### Registration/selection — provider-identifier keyed, NOT chained, NOT first-match
Unlike every other capability in this document, thinking appliers are **not** invoked via
`h.activeRecords()` iteration at request time. Instead the host maintains a **registry keyed by
provider name** (`internal/thinking/apply.go`), refreshed whenever the plugin snapshot changes:

```go
// internal/pluginhost/adapters.go
func (h *Host) refreshThinkingProviders(records []capabilityRecord) {
    thinking.ClearPluginProviders()
    for _, record := range records {
        applier := record.plugin.Capabilities.ThinkingApplier
        if applier == nil || h.isPluginFused(record.id) { continue }
        provider, okProvider := h.callThinkingIdentifier(record, applier)
        if !okProvider { continue }
        thinking.RegisterPluginProvider(record.id, provider, record.priority, &thinkingAdapter{...})
    }
}
```

```go
// internal/thinking/apply.go
func RegisterPluginProvider(owner string, name string, priority int, applier ProviderApplier) bool {
    name = normalizedProviderName(name)
    if _, native := nativeProviderAppliers[name]; native {
        return false // a built-in/native applier for this provider ALWAYS wins; plugin registration is rejected outright
    }
    current, exists := pluginProviderAppliers[name]
    if exists && (current.priority > priority || (current.priority == priority && current.owner <= owner)) {
        return false // existing registration keeps priority unless the new one strictly outranks it
    }
    pluginProviderAppliers[name] = pluginProviderApplier{owner: owner, priority: priority, applier: applier}
    return true
}
```
Key takeaways:
- **Built-in/native providers always win.** If the host already has a native thinking applier for a
  provider name, a plugin's `RegisterPluginProvider` call for that same name is a no-op — your
  plugin's applier is silently never used for that provider.
- Among competing **plugins** claiming the same provider identifier, the one with the **higher
  `priority`** (from `plugins.configs.<id>.priority`) wins the registration slot.
- At equal priority, whichever registration is processed **and has the strictly earlier
  (lexicographically smaller) plugin ID** wins (`current.owner <= owner` blocks the *new* one from
  overwriting when the new owner's ID is alphabetically ≥ the current owner — i.e. smaller ID wins
  the tie).
- Registration is keyed **purely by provider identifier string** returned from
  `thinking.identifier` — there's no per-model or per-format dimension here.

### Development notes
- A plugin should only handle the provider its own `thinking.identifier` returns — don't try to
  intercept other providers' thinking payloads.
- **Do not bypass host thinking validation** — treat `Config` as already canonical/valid; don't
  re-derive it from the raw request body.
- **Do not** do request translation, credential selection, or upstream execution inside a thinking
  applier — this hook's only job is "canonical config → provider JSON fields."

### Vendored example
`${CLAUDE_PLUGIN_ROOT}/references/upstream/examples/thinking/go/main.go` — the dedicated
single-purpose example:
```go
case "thinking.identifier":
    return okEnvelopeJSON("{\"identifier\":\"example-thinking-go\"}")
case "thinking.apply":
    return okEnvelopeJSON("{\"Body\":\"...thinking_applied_by:example-thinking-go...\"}")
```
A real implementation decodes `Body` into a JSON map, writes provider-specific thinking fields
based on `req.Config.Mode`/`.Budget`/`.Level`, and re-marshals:
```go
func applyThinking(raw []byte) ([]byte, error) {
    var req pluginapi.ThinkingApplyRequest
    json.Unmarshal(raw, &req)
    body := map[string]any{}
    json.Unmarshal(req.Body, &body)
    body["plugin_example_thinking"] = map[string]any{
        "mode": req.Config.Mode, "budget": req.Config.Budget, "level": req.Config.Level,
    }
    out, _ := json.Marshal(body)
    return okEnvelope(pluginapi.PayloadResponse{Body: out})
}
```

---

## 12. Cross-cutting gotchas for all eight (translator/normalizer/interceptor) capabilities + thinking applier

1. **Panics permanently disable ("fuse") a plugin.** Every dispatch path wraps the plugin call in a
   `recover()` and, on panic, calls `h.fusePlugin(id, method, recovered)`:
   ```go
   func (h *Host) fusePlugin(id, method string, recovered any) {
       h.fused[id] = fmt.Sprintf("%s panic: %v", method, recovered)
       thinking.UnregisterPluginProviders(id)
       log.Errorf("pluginhost: plugin panic recovered: %v\n%s", recovered, debug.Stack())
   }
   ```
   Once fused, `h.isPluginFused(id)` short-circuits **every** future capability call for that
   plugin instance (checked at the top of essentially every adapter function shown above) — the
   plugin is effectively dead for the remainder of that snapshot/process lifetime, not just for the
   failing request. **Return an error from your handler; never let a panic escape.**

2. **Empty return = "I didn't handle this" is a load-bearing convention.** Across translators,
   normalizers, and interceptors alike, an empty `Body` (and/or a returned `error`) is how a plugin
   signals "pass through unchanged" / "not applicable" so the chain or fallback logic can proceed.
   Returning an empty body when you *meant* to clear content will be silently ignored, not treated
   as "set body to empty."

3. **Translators short-circuit on first success; everything else runs the full chain.** If you
   register two plugins that both declare `request_translator` for overlapping format pairs, only
   the higher-priority one's output is ever used for a given request — the other is never invoked.
   Normalizers and interceptors, by contrast, **all** run, each seeing the prior one's output —
   ordering (via `priority`) determines the pipeline order, not which one "wins."

4. **Only interceptors touch HTTP headers.** Translators and normalizers operate purely on `Body`.
   If a task needs header manipulation (auth injection, `X-Plugin` markers, clearing an upstream
   header), it must be a `request_interceptor` / `response_interceptor` /
   `response_stream_interceptor`, not a normalizer — the docs call this out explicitly for
   `response_after_translator`.

5. **`response_interceptor` never fires for streaming responses** — use
   `response_stream_interceptor` for those, which has a completely different per-chunk contract
   (including the `ChunkIndex == -1` header-init call and the bounded `HistoryChunks` window).

6. **Recursion guard on nested host-callback model calls.** If your plugin issues a nested request
   via `host.model.*` and passes `host_callback_id`, the host skips *your own* request/response/
   stream interceptors for that nested call (but not other plugins' interceptors) to prevent
   infinite self-recursion.

7. **`Metadata` in interceptor requests is read-only.** It's a best-effort clone of a host context
   snapshot — treat it as JSON-like inspection data, not a channel for passing state forward.

8. **Never make blocking/high-latency external calls inside a per-chunk stream interceptor** — it
   runs on the hot path once per SSE chunk; do that kind of work in a normalizer/translator/request
   interceptor stage instead, where it runs once per request.

9. **`response_before_translator` and `response_after_translator` share one Go interface**
   (`ResponseNormalizer`) — a single struct can implement `NormalizeResponse` once and be wired to
   both `Capabilities.ResponseBeforeTranslator` and `Capabilities.ResponseAfterTranslator` fields,
   distinguishing behavior (if needed) via the request's `FromFormat`/`ToFormat`/`Stream`, since the
   RPC-layer method name (`response.normalize_before` vs `response.normalize_after`) is what the
   *host* uses to pick which stage to call — a raw-RPC plugin implementation dispatches on that
   method string itself (see the vendored `response-normalizer` example).

10. **Thinking applier registration is provider-identifier-keyed, and native providers always
    beat plugin providers** — unlike the request/response pipeline capabilities, there's no
    per-request chaining or first-match search; it's a single global registry entry per provider
    name, refreshed whenever the plugin snapshot changes, and a plugin can never override a
    built-in provider's thinking applier no matter its `priority`.

---

## 13. Quick-reference table of every method name and its Go request/response types

| Capability flag | RPC method(s) | Request type | Response type | Selection semantics | Vendored example |
|---|---|---|---|---|---|
| `request_translator` | `request.translate` | `RequestTransformRequest` | `PayloadResponse` | first-match-wins | `examples/request-translator/go` |
| `request_normalizer` | `request.normalize` | `RequestTransformRequest` | `PayloadResponse` | chained | `examples/codex-service-tier/go` (real); `examples/request-normalizer/go` (stub) |
| `response_translator` | `response.translate` | `ResponseTransformRequest` | `PayloadResponse` | first-match-wins | `examples/response-translator/go` |
| `response_before_translator` | `response.normalize_before` | `ResponseTransformRequest` | `PayloadResponse` | chained | `examples/response-normalizer/go` |
| `response_after_translator` | `response.normalize_after` | `ResponseTransformRequest` | `PayloadResponse` | chained | `examples/response-normalizer/go` |
| `request_interceptor` | `request.intercept_before`, `request.intercept_after` | `RequestInterceptRequest` | `RequestInterceptResponse` | chained (headers + body) | none — implement from the interface |
| `response_interceptor` | `response.intercept_after` | `ResponseInterceptRequest` | `ResponseInterceptResponse` | chained (headers + body), non-streaming only | none — implement from the interface |
| `response_stream_interceptor` | `response.intercept_stream_chunk` | `StreamChunkInterceptRequest` | `StreamChunkInterceptResponse` | chained (headers + body), per-chunk, streaming only | none — implement from the interface |
| `thinking_applier` | `thinking.identifier`, `thinking.apply` | `ThinkingApplyRequest` | `PayloadResponse` | single registry entry per provider identifier; native > plugin; then priority, then plugin-ID tiebreak | `examples/thinking/go` |

All defined in `${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginapi-types.go`; all method-name
constants in `${CLAUDE_PLUGIN_ROOT}/references/upstream/pluginabi-types.go`; all host-side
dispatch/ordering logic in `internal/pluginhost/adapters.go` and
`internal/pluginhost/snapshot.go` (host source, not vendored — cited for context).
