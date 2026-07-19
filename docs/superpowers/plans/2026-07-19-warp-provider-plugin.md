# Warp Provider Plugin — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `projects/warp/` — a CLIProxyAPI native Go plugin that makes Warp AI a provider, spending Warp subscription credits via `app.warp.dev/ai/multi-agent`.

**Architecture:** Thin C-ABI adapter (`main.go`) over an all-logic `internal/core` package (mirrors `projects/opencode-go/`). Declares `auth_provider` + `executor` (format `chat-completions` in/out, scope `oauth`) + `model_registrar` + `command_line_plugin`. The executor maps `chat-completions JSON ⟷ warp.multi_agent.v1` protobuf, POSTs over HTTP/2, parses the protobuf-over-SSE response, and re-emits `chat-completions` (streamed via `host.stream.emit`).

**Tech Stack:** Go 1.26 + CGO (c-shared); `github.com/router-for-me/CLIProxyAPI/v7 v7.2.88` (SDK types `pluginapi`/`pluginabi`); `github.com/warpdotdev/warp-proto-apis/apis/multi_agent/v1/gen/go` (protobuf bindings, Editions-2023 **Opaque API**); `google.golang.org/protobuf`; stdlib `net/http` (auto HTTP/2).

Spec: `docs/superpowers/specs/2026-07-19-warp-provider-plugin-design.md`.

## Global Constraints

- **Module path:** `github.com/pragmaticgrowth/cpa-plugins/projects/warp`. `go 1.26.0`.
- **Plugin ID / provider key:** `warp`. Output artifact `warp.<dylib|so|dll>`.
- **Build:** `CGO_ENABLED=1 go build -buildmode=c-shared -o warp.dylib .` must export `cliproxy_plugin_init`.
- **Unit tests run without CGO:** `go test ./internal/core/...` (pure Go; the c-shared build is separate).
- **Never panic across the ABI** — every handler returns an error; recover in goroutines and `host.stream.close` with the message.
- **Opaque protobuf API:** construct messages with `<Type>_builder{...}.Build()` and read with `Get<Field>()`. Use `google.golang.org/protobuf/proto` for `proto.String/Bool`, `proto.Marshal`, `proto.Unmarshal`. **Exact generated type/accessor names are confirmed in Task 2 and reused verbatim thereafter.**
- **Credits switch:** `Settings.ApiKeys.allow_use_of_warp_credits = true`, all BYOK key strings empty.
- **AI endpoint constants:** URL `https://app.warp.dev/ai/multi-agent`; refresh `https://app.warp.dev/proxy/token?key=<firebaseKey>`; `firebaseKey = "AIzaSyBdy3O3S9hrdayLJxJ7mriBR4qgUaUygAs"`.
- **Required AI headers (config-overridable):** `content-type: application/x-protobuf`, `accept: text/event-stream`, `authorization: Bearer <jwt>`, `x-warp-client-version` (default `v0.2025.08.06.08.12.stable_02`), `x-warp-os-category`/`-os-name`/`-os-version` (defaults `Windows`/`Windows`/`11 (26100)`).
- **SSE decode:** each `data:` line → try base64url (pad to %4) then hex; `data: [DONE]` ends the stream.
- **License:** plugin is AGPL-3.0 (imports Warp's AGPL bindings) — state in `README.md`.

---

### Task 1: Project skeleton, build, and 4-capability registration

**Files:**
- Create: `projects/warp/go.mod`
- Create: `projects/warp/main.go`
- Create: `projects/warp/internal/core/dispatch.go`
- Create: `projects/warp/internal/core/register.go`
- Create: `projects/warp/internal/core/config.go`
- Create: `projects/warp/internal/core/register_test.go`
- Create: `projects/warp/internal/core/config_test.go`
- Create: `projects/warp/Makefile`, `projects/warp/.gitignore`, `projects/warp/config.snippet.yaml`, `projects/warp/warp.json.example`, `projects/warp/README.md`

**Interfaces:**
- Produces: `core.Dispatch(method string, request []byte, host HostBridge) (json.RawMessage, error)`; `core.HostBridge` interface; `core.Config` + `core.CurrentConfig()`; `core.registerResponse()`.

- [ ] **Step 1: Write `go.mod`**

```go
module github.com/pragmaticgrowth/cpa-plugins/projects/warp

go 1.26.0

require (
	github.com/router-for-me/CLIProxyAPI/v7 v7.2.88
	google.golang.org/protobuf v1.36.6
	gopkg.in/yaml.v3 v3.0.1
)
```
(The `warp-proto-apis` require is added in Task 2 after `go get`.)

- [ ] **Step 2: Write `internal/core/config.go`**

```go
package core

import (
	"sync/atomic"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Enabled        bool   `yaml:"enabled"`
	Priority       int    `yaml:"priority"`
	UseWarpCredits bool   `yaml:"use_warp_credits"`
	ModelPrefix    string `yaml:"model_prefix"`
	ClientVersion  string `yaml:"client_version"`
	OSCategory     string `yaml:"os_category"`
	OSName         string `yaml:"os_name"`
	OSVersion      string `yaml:"os_version"`
}

func defaultConfig() Config {
	return Config{
		Enabled:        true,
		UseWarpCredits: true,
		ModelPrefix:    "warp/",
		ClientVersion:  "v0.2025.08.06.08.12.stable_02",
		OSCategory:     "Windows",
		OSName:         "Windows",
		OSVersion:      "11 (26100)",
	}
}

var currentConfig atomic.Value // Config

func init() { currentConfig.Store(defaultConfig()) }

func CurrentConfig() Config { return currentConfig.Load().(Config) }

// applyConfigYAML parses plugin config_yaml bytes over the defaults.
func applyConfigYAML(raw []byte) error {
	cfg := defaultConfig()
	if len(raw) > 0 {
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return err
		}
	}
	if cfg.ModelPrefix == "" {
		cfg.ModelPrefix = "warp/"
	}
	currentConfig.Store(cfg)
	return nil
}
```

- [ ] **Step 3: Write `internal/core/config_test.go` (failing)**

```go
package core

import "testing"

func TestApplyConfigYAML_Defaults(t *testing.T) {
	if err := applyConfigYAML(nil); err != nil {
		t.Fatalf("nil config: %v", err)
	}
	c := CurrentConfig()
	if !c.UseWarpCredits || c.ModelPrefix != "warp/" || c.ClientVersion == "" {
		t.Fatalf("bad defaults: %+v", c)
	}
}

func TestApplyConfigYAML_Override(t *testing.T) {
	err := applyConfigYAML([]byte("use_warp_credits: false\nmodel_prefix: \"w:\"\nclient_version: \"v9\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	c := CurrentConfig()
	if c.UseWarpCredits || c.ModelPrefix != "w:" || c.ClientVersion != "v9" {
		t.Fatalf("override failed: %+v", c)
	}
}
```

- [ ] **Step 4: Run and watch it fail, then pass**

Run: `cd projects/warp && go test ./internal/core/ -run TestApplyConfigYAML -v`
Expected: FAIL (no `applyConfigYAML`) before Step 2 is in place; PASS after. (Write Step 2 then Step 3 in that order; run to confirm PASS.)

- [ ] **Step 5: Write `internal/core/register.go`**

```go
package core

import (
	"encoding/json"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type registration struct {
	SchemaVersion uint32             `json:"schema_version"`
	Metadata      pluginapi.Metadata `json:"metadata"`
	Capabilities  capabilities       `json:"capabilities"`
}

type capabilities struct {
	AuthProvider          bool     `json:"auth_provider"`
	Executor              bool     `json:"executor"`
	ExecutorModelScope    string   `json:"executor_model_scope,omitempty"`
	ExecutorInputFormats  []string `json:"executor_input_formats,omitempty"`
	ExecutorOutputFormats []string `json:"executor_output_formats,omitempty"`
	ModelRegistrar        bool     `json:"model_registrar"`
	CommandLinePlugin     bool     `json:"command_line_plugin"`
}

type lifecycleRequest struct {
	ConfigYAML []byte `json:"config_yaml"`
}

func handleLifecycle(raw []byte) (json.RawMessage, error) {
	var req lifecycleRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
	}
	if err := applyConfigYAML(req.ConfigYAML); err != nil {
		return nil, err
	}
	return registerResponse()
}

func registerResponse() (json.RawMessage, error) {
	reg := registration{
		SchemaVersion: 1,
		Metadata: pluginapi.Metadata{
			Name:             "warp",
			Version:          "0.1.0",
			Author:           "pragmaticgrowth",
			GitHubRepository: "https://github.com/pragmaticgrowth/cpa-plugins",
		},
		Capabilities: capabilities{
			AuthProvider:          true,
			Executor:              true,
			ExecutorModelScope:    "oauth",
			ExecutorInputFormats:  []string{"chat-completions"},
			ExecutorOutputFormats: []string{"chat-completions"},
			ModelRegistrar:        true,
			CommandLinePlugin:     true,
		},
	}
	return json.Marshal(reg)
}
```

- [ ] **Step 6: Write `internal/core/dispatch.go`**

```go
package core

import (
	"encoding/json"
	"fmt"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// HostBridge is the subset of host callbacks the plugin needs.
type HostBridge interface {
	StreamEmit(streamID string, payload []byte) error
	StreamClose(streamID, errMsg string) error
	Log(level, msg string)
}

func okEnvelope(result any) (json.RawMessage, error) {
	body, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	env, err := json.Marshal(pluginabi.Envelope{OK: true, Result: body})
	if err != nil {
		return nil, err
	}
	return env, nil
}

// Dispatch routes an RPC method to its handler and returns a marshaled envelope.
func Dispatch(method string, request []byte, host HostBridge) (json.RawMessage, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		reg, err := handleLifecycle(request)
		if err != nil {
			return nil, err
		}
		return okEnvelope(json.RawMessage(reg))
	case pluginabi.MethodPluginShutdown:
		return okEnvelope(map[string]any{})
	// auth / models / cli / executor cases are added in later tasks.
	default:
		return nil, fmt.Errorf("unknown method %q", method)
	}
}

var _ = pluginapi.Metadata{} // keep import until later tasks use it
```

> Verify `pluginabi.Envelope`'s JSON tags in `references/upstream/pluginabi-types.go`; if `Result` is `json.RawMessage` there, marshal accordingly (the shape above assumes `Envelope{OK bool; Result json.RawMessage; Error *...}`). Adjust `okEnvelope` to match the real struct.

- [ ] **Step 7: Write `internal/core/register_test.go`**

```go
package core

import (
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

func TestRegister_DeclaresFourCapabilities(t *testing.T) {
	out, err := Dispatch(pluginabi.MethodPluginRegister, []byte(`{"config_yaml":""}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(out, &env); err != nil || !env.OK {
		t.Fatalf("bad envelope: %v ok=%v", err, env.OK)
	}
	var reg registration
	if err := json.Unmarshal(env.Result, &reg); err != nil {
		t.Fatal(err)
	}
	c := reg.Capabilities
	if !c.AuthProvider || !c.Executor || !c.ModelRegistrar || !c.CommandLinePlugin {
		t.Fatalf("missing capability: %+v", c)
	}
	if c.ExecutorModelScope != "oauth" ||
		len(c.ExecutorInputFormats) != 1 || c.ExecutorInputFormats[0] != "chat-completions" {
		t.Fatalf("bad executor decl: %+v", c)
	}
}
```

Run: `go test ./internal/core/ -run TestRegister -v` → PASS.

- [ ] **Step 8: Write `main.go` (thin ABI adapter)**

Copy `projects/opencode-go/main.go` verbatim, then change only: (a) the import of `internal/core` to the warp module path; (b) `cliproxyPluginCall` calls `core.Dispatch(C.GoString(method), requestBytes, hostBridge{})`; (c) add the `hostBridge` type and the stream/log wrappers below. Keep the entire `#include`/`callHost`/`writeResponse`/`cliproxy_plugin_init` machinery identical.

Append to `main.go`:

```go
type hostBridge struct{}

type rpcStreamEmit struct {
	StreamID string `json:"stream_id"`
	Payload  []byte `json:"payload,omitempty"`
}
type rpcStreamClose struct {
	StreamID string `json:"stream_id"`
	Error    string `json:"error,omitempty"`
}

func (hostBridge) StreamEmit(id string, payload []byte) error {
	_, err := callHost(pluginabi.MethodHostStreamEmit, rpcStreamEmit{StreamID: id, Payload: payload})
	return err
}
func (hostBridge) StreamClose(id, errMsg string) error {
	_, err := callHost(pluginabi.MethodHostStreamClose, rpcStreamClose{StreamID: id, Error: errMsg})
	return err
}
func (hostBridge) Log(level, msg string) {
	_, _ = callHost(pluginabi.MethodHostLog, map[string]string{"level": level, "message": msg})
}
```

- [ ] **Step 9: Write `Makefile`, `.gitignore`, `config.snippet.yaml`, `warp.json.example`, `README.md`**

`Makefile` — copy opencode-go's, set `PLUGIN_ID := warp`.
`.gitignore` — `warp.dylib` / `.so` / `.dll` / `.h`.
`config.snippet.yaml`:
```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    warp:
      enabled: true
      priority: 10
      use_warp_credits: true
      model_prefix: "warp/"
      client_version: "v0.2025.08.06.08.12.stable_02"
```
`warp.json.example`:
```json
{ "type": "warp", "refresh_token": "PASTE_FIREBASE_REFRESH_TOKEN", "access_token": "", "expires_at": "1970-01-01T00:00:00Z" }
```
`README.md` — one-paragraph description + build/install steps + **"License: AGPL-3.0 (imports Warp's AGPL-3.0 protobuf bindings). Unofficial, unsupported integration."**

- [ ] **Step 10: Build and verify ABI export**

Run: `cd projects/warp && go mod tidy && make build && nm -gU warp.dylib | grep cliproxy_plugin_init`
Expected: `go test ./internal/core/...` PASS; build succeeds; `nm` prints `_cliproxy_plugin_init`.

- [ ] **Step 11: Commit**

```bash
git add projects/warp
git commit -m "feat(warp): skeleton plugin — 4-capability registration + config"
```

---

### Task 2: Protobuf bindings spike (de-risk the Opaque API)

**Files:**
- Modify: `projects/warp/go.mod` (add `warp-proto-apis` require)
- Create: `projects/warp/internal/core/warppb/alias.go`
- Create: `projects/warp/internal/core/proto_spike_test.go`

**Interfaces:**
- Produces: import alias `warppb` for the bindings; **a recorded list of exact builder/getter names** (in a comment block in `alias.go`) reused by Tasks 7–10.

- [ ] **Step 1: Add the dependency**

Run: `cd projects/warp && go get github.com/warpdotdev/warp-proto-apis/apis/multi_agent/v1/gen/go@main && go mod tidy`
Expected: resolves; `go.sum` updated. If codegen is too new for the toolchain, fall back: vendor the `.proto` files and regenerate with a current `protoc-gen-go` (record the command in `alias.go`).

- [ ] **Step 2: Write `internal/core/warppb/alias.go`**

```go
// Package warppb re-exports Warp's multi_agent v1 protobuf bindings.
// Bindings are Editions-2023 / Opaque API: construct with <Type>_builder{...}.Build(),
// read with Get<Field>(). Confirmed accessor names (fill in during Step 4):
//   Request builder:                warppb.Request_builder{...}.Build()
//   Settings builder:               warppb.Request_Settings_builder{...}
//   ModelConfig builder:            warppb.Request_Settings_ModelConfig_builder{Base: proto.String(...)}
//   ApiKeys builder:                warppb.Request_Settings_ApiKeys_builder{AllowUseOfWarpCredits: proto.Bool(true)}
//   Input builder:                  warppb.Request_Input_builder{UserInputs: ...}
//   UserInputs builder:             warppb.Request_Input_UserInputs_builder{Inputs: []*...UserInput{...}}
//   UserInput builder:              warppb.Request_Input_UserInputs_UserInput_builder{UserQuery: ...}
//   UserQuery builder:              warppb.Request_Input_UserQuery_builder{Query: proto.String(...), ReferencedAttachments: map[string]*Attachment{...}}
//   Metadata builder:               warppb.Request_Metadata_builder{ConversationId: proto.String(...)}
//   TaskContext/Task/Message:       from task.proto (Message_builder, Message_UserQuery_builder, Message_AgentOutput_builder)
//   ResponseEvent getters:          ev.GetInit(), ev.GetClientActions(), ev.GetFinished()
//   ClientAction getters:           a.GetAppendToMessageContent(), a.GetAddMessagesToTask()
//   AgentOutput getter:             msg.GetAgentOutput().GetText()
// CORRECT ANY NAME ABOVE THAT THE REAL GENERATED CODE DISAGREES WITH.
package warppb

import v1 "github.com/warpdotdev/warp-proto-apis/apis/multi_agent/v1/gen/go"

// Re-export the whole package via a dot-free alias for callers.
// (Callers import this package and use warppb.<Type>; or import v1 directly.)
```
> Practical note: instead of re-exporting types one by one, callers may simply import the generated package directly as `warppb "github.com/warpdotdev/warp-proto-apis/apis/multi_agent/v1/gen/go"`. Use whichever the real package name allows; the alias file exists to hold the confirmed-accessor comment block.

- [ ] **Step 3: Write `internal/core/proto_spike_test.go`**

```go
package core

import (
	"testing"

	"google.golang.org/protobuf/proto"
	warppb "github.com/warpdotdev/warp-proto-apis/apis/multi_agent/v1/gen/go"
)

func TestSpike_BuildAndRoundTripRequest(t *testing.T) {
	req := warppb.Request_builder{
		Settings: warppb.Request_Settings_builder{
			ModelConfig: warppb.Request_Settings_ModelConfig_builder{
				Base: proto.String("claude-4-sonnet"),
			}.Build(),
		}.Build(),
	}.Build()

	raw, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var got warppb.Request
	if err := proto.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if base := got.GetSettings().GetModelConfig().GetBase(); base != "claude-4-sonnet" {
		t.Fatalf("round-trip base = %q", base)
	}
}

func TestSpike_DecodeResponseEvent(t *testing.T) {
	ev := warppb.ResponseEvent_builder{
		Init: warppb.ResponseEvent_StreamInit_builder{
			ConversationId: proto.String("conv-1"),
		}.Build(),
	}.Build()
	raw, _ := proto.Marshal(ev)
	var got warppb.ResponseEvent
	if err := proto.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.GetInit().GetConversationId() != "conv-1" {
		t.Fatal("init conversation id not decoded")
	}
}
```

- [ ] **Step 4: Run, fix accessor names, record them**

Run: `go test ./internal/core/ -run TestSpike -v`
Expected: PASS. If any builder/getter name mismatches, `go build` will name the correct symbol — fix the test to match, then update the confirmed-accessor comment block in `warppb/alias.go` to the real names. These names are now authoritative for Tasks 7–10.

- [ ] **Step 5: Commit**

```bash
git add projects/warp/go.mod projects/warp/go.sum projects/warp/internal/core
git commit -m "test(warp): protobuf bindings spike — confirm Opaque API accessors"
```

---

### Task 3: Credential model, JWT expiry, token refresh

**Files:**
- Create: `projects/warp/internal/core/credential.go`
- Create: `projects/warp/internal/core/token.go`
- Create: `projects/warp/internal/core/credential_test.go`
- Create: `projects/warp/internal/core/token_test.go`

**Interfaces:**
- Produces: `Credential` struct; `Credential.NextRefresh() time.Time`; `jwtExpiry(token string) (time.Time, bool)`; `RefreshAccessToken(client *http.Client, endpoint, firebaseKey, refreshToken string) (accessToken string, expiresAt time.Time, err error)`; consts `FirebaseKey`, `refreshEndpoint`.

- [ ] **Step 1: Write `credential.go`**

```go
package core

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

const FirebaseKey = "AIzaSyBdy3O3S9hrdayLJxJ7mriBR4qgUaUygAs"

type Credential struct {
	Type         string    `json:"type"`
	RefreshToken string    `json:"refresh_token"`
	AccessToken  string    `json:"access_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	Email        string    `json:"email,omitempty"`
}

// NextRefresh returns 5 minutes before expiry (or now-ish if unknown/expired).
func (c Credential) NextRefresh() time.Time {
	if c.ExpiresAt.IsZero() {
		return time.Now()
	}
	return c.ExpiresAt.Add(-5 * time.Minute)
}

// jwtExpiry decodes a JWT's "exp" claim without verifying the signature.
func jwtExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0).UTC(), true
}
```

- [ ] **Step 2: Write `credential_test.go` (failing), then run to PASS**

```go
package core

import (
	"encoding/base64"
	"testing"
	"time"
)

func TestJWTExpiry(t *testing.T) {
	exp := time.Now().Add(time.Hour).Unix()
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":` + itoa(exp) + `}`))
	tok := "aaa." + payload + ".bbb"
	got, ok := jwtExpiry(tok)
	if !ok || got.Unix() != exp {
		t.Fatalf("exp mismatch ok=%v got=%v", ok, got.Unix())
	}
}

func itoa(v int64) string { return time.Unix(v, 0).UTC().Format("") /* replaced below */ }
```
> Replace the placeholder `itoa` with `strconv.FormatInt(v,10)` (import `strconv`) — written inline here to avoid an unused import; use `strconv.FormatInt` in the real test.

Run: `go test ./internal/core/ -run TestJWTExpiry -v` → PASS.

- [ ] **Step 3: Write `token.go`**

```go
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const refreshEndpoint = "https://app.warp.dev/proxy/token"

// RefreshAccessToken exchanges a Firebase refresh token for a fresh access JWT.
func RefreshAccessToken(client *http.Client, endpoint, firebaseKey, refreshToken string) (string, time.Time, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)

	u := endpoint + "?key=" + url.QueryEscape(firebaseKey)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("refresh status %d: %s", resp.StatusCode, string(body))
	}

	// Decode permissively — only access_token is guaranteed.
	var out struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
		ExpiresIn   string `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", time.Time{}, fmt.Errorf("decode refresh: %w", err)
	}
	access := out.AccessToken
	if access == "" {
		access = out.IDToken
	}
	if access == "" {
		return "", time.Time{}, fmt.Errorf("refresh response missing access_token")
	}
	// Prefer the JWT's own exp; fall back to expires_in; else +55m.
	exp, ok := jwtExpiry(access)
	if !ok {
		if secs, e := strconv.Atoi(out.ExpiresIn); e == nil && secs > 0 {
			exp = time.Now().Add(time.Duration(secs) * time.Second)
		} else {
			exp = time.Now().Add(55 * time.Minute)
		}
	}
	return access, exp, nil
}
```

- [ ] **Step 4: Write `token_test.go` (httptest), run to PASS**

```go
package core

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRefreshAccessToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "RT" {
			w.WriteHeader(400)
			return
		}
		// access_token with an exp claim ~1h out
		w.Write([]byte(`{"access_token":"aa.eyJleHAiOjQ4MDAwMDAwMDB9.bb","expires_in":"3600"}`))
	}))
	defer srv.Close()

	access, exp, err := RefreshAccessToken(srv.Client(), srv.URL, "KEY", "RT")
	if err != nil || access == "" {
		t.Fatalf("refresh err=%v access=%q", err, access)
	}
	if exp.IsZero() {
		t.Fatal("expiry not set")
	}
	_ = strings.TrimSpace
}
```

Run: `go test ./internal/core/ -run 'TestRefreshAccessToken|TestJWTExpiry' -v` → PASS.

- [ ] **Step 5: Commit**

```bash
git add projects/warp/internal/core/{credential,token}*.go
git commit -m "feat(warp): credential model + Firebase token refresh"
```

---

### Task 4: `auth_provider` handlers

**Files:**
- Create: `projects/warp/internal/core/auth.go`
- Create: `projects/warp/internal/core/auth_test.go`
- Modify: `projects/warp/internal/core/dispatch.go` (add auth cases)

**Interfaces:**
- Consumes: `Credential`, `RefreshAccessToken`, `pluginapi.AuthData`, `pluginapi.AuthParseRequest`, `pluginapi.AuthRefreshRequest/Response`.
- Produces: `handleAuthParse/handleAuthRefresh/handleAuthLogin*`; package var `warpHTTPClient *http.Client` (shared, HTTP/2).

- [ ] **Step 1: Write `auth.go`**

```go
package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// warpHTTPClient is used for all Warp network I/O (auto HTTP/2 over TLS).
var warpHTTPClient = &http.Client{Timeout: 0} // no global timeout (streaming); per-call ctx bounds refresh

func authIdentifier() (json.RawMessage, error) {
	return okEnvelope(map[string]string{"identifier": "warp"})
}

func handleAuthParse(raw []byte) (json.RawMessage, error) {
	var req pluginapi.AuthParseRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	var cred Credential
	if err := json.Unmarshal(req.RawJSON, &cred); err != nil {
		return nil, err
	}
	if cred.Type != "warp" || cred.RefreshToken == "" {
		return okEnvelope(pluginapi.AuthParseResponse{Handled: false})
	}
	auth := credentialToAuthData(req.FileName, cred)
	return okEnvelope(pluginapi.AuthParseResponse{Handled: true, Auth: auth})
}

func handleAuthRefresh(raw []byte) (json.RawMessage, error) {
	var req pluginapi.AuthRefreshRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	var cred Credential
	if err := json.Unmarshal(req.StorageJSON, &cred); err != nil {
		return nil, err
	}
	access, exp, err := RefreshAccessToken(warpHTTPClient, refreshEndpoint, FirebaseKey, cred.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("warp refresh: %w", err)
	}
	cred.AccessToken = access
	cred.ExpiresAt = exp
	auth := credentialToAuthData(req.AuthID+".json", cred)
	auth.ID = req.AuthID
	return okEnvelope(pluginapi.AuthRefreshResponse{Auth: auth, NextRefreshAfter: cred.NextRefresh()})
}

func handleAuthLoginStart(raw []byte) (json.RawMessage, error) {
	return okEnvelope(pluginapi.AuthLoginStartResponse{
		Provider:  "warp",
		URL:       "run: cli-proxy-api --warp-login   (imports your Warp credential)",
		State:     "warp-cli-login",
		ExpiresAt: time.Now().Add(10 * time.Minute),
	})
}

func handleAuthLoginPoll(raw []byte) (json.RawMessage, error) {
	return okEnvelope(pluginapi.AuthLoginPollResponse{
		Status:  pluginapi.AuthLoginStatusError,
		Message: "interactive login not supported; run `cli-proxy-api --warp-login`",
	})
}

func credentialToAuthData(fileName string, cred Credential) pluginapi.AuthData {
	storage, _ := json.Marshal(cred)
	id := "warp"
	if cred.Email != "" {
		id = "warp-" + cred.Email
	}
	return pluginapi.AuthData{
		Provider:         "warp",
		ID:               id,
		FileName:         fileName,
		Label:            "Warp",
		StorageJSON:      storage,
		NextRefreshAfter: cred.NextRefresh(),
	}
}
```

> Confirm `pluginapi.AuthParseResponse` has field `Handled` and `Auth` (it does per `references/upstream/pluginapi-types.go:251`). Confirm `AuthLoginStartResponse`/`AuthLoginPollResponse`/`AuthLoginStatusError` names against the same file.

- [ ] **Step 2: Wire auth methods into `dispatch.go`**

Add cases:
```go
	case pluginabi.MethodAuthIdentifier:
		return authIdentifier()
	case pluginabi.MethodAuthParse:
		return handleAuthParse(request)
	case pluginabi.MethodAuthRefresh:
		return handleAuthRefresh(request)
	case pluginabi.MethodAuthLoginStart:
		return handleAuthLoginStart(request)
	case pluginabi.MethodAuthLoginPoll:
		return handleAuthLoginPoll(request)
```

- [ ] **Step 3: Write `auth_test.go`**

```go
package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

func TestAuthParse_AcceptsWarpCredential(t *testing.T) {
	req := `{"Provider":"warp","FileName":"warp.json","RawJSON":` +
		jsonString(`{"type":"warp","refresh_token":"RT"}`) + `}`
	out, err := Dispatch(pluginabi.MethodAuthParse, []byte(req), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !resultHas(t, out, `"Handled":true`) {
		t.Fatalf("expected Handled true: %s", out)
	}
}

func TestAuthParse_RejectsForeign(t *testing.T) {
	req := `{"Provider":"warp","RawJSON":` + jsonString(`{"type":"other"}`) + `}`
	out, _ := Dispatch(pluginabi.MethodAuthParse, []byte(req), nil)
	if resultHas(t, out, `"Handled":true`) {
		t.Fatal("should not handle foreign credential")
	}
}

func TestAuthRefresh_UpdatesToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"aa.eyJleHAiOjQ4MDAwMDAwMDB9.bb"}`))
	}))
	defer srv.Close()
	old := refreshEndpointVar
	refreshEndpointVar = srv.URL // see note
	defer func() { refreshEndpointVar = old }()
	warpHTTPClient = srv.Client()

	req := `{"AuthID":"warp","StorageJSON":` + jsonString(`{"type":"warp","refresh_token":"RT"}`) + `}`
	out, err := Dispatch(pluginabi.MethodAuthRefresh, []byte(req), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !resultHas(t, out, `"access_token":"aa.`) {
		t.Fatalf("token not refreshed: %s", out)
	}
}

// helpers
func jsonString(s string) string { b, _ := json.Marshal(s); return string(b) }
func resultHas(t *testing.T, env json.RawMessage, needle string) bool {
	t.Helper()
	return len(env) > 0 && containsRaw(env, needle)
}
```
> To make the refresh endpoint injectable for the test, change `token.go`'s call site in `handleAuthRefresh` to use a package var `refreshEndpointVar = refreshEndpoint` instead of the const. Add `containsRaw` = `strings.Contains(string(env), needle)`. (StorageJSON is base64 on the wire via Go's default `[]byte` JSON encoding — assert on the decoded result instead if needed; simplest is to decode the envelope→result→Auth.StorageJSON and json-unmarshal to `Credential`, then assert `AccessToken` has the prefix. Prefer that stronger assertion.)

- [ ] **Step 4: Run → PASS**

Run: `go test ./internal/core/ -run TestAuth -v`

- [ ] **Step 5: Commit**

```bash
git add projects/warp/internal/core/{auth.go,auth_test.go,dispatch.go,token.go}
git commit -m "feat(warp): auth_provider (parse/refresh/login stubs)"
```

---

### Task 5: `command_line_plugin` — `--warp-login`

**Files:**
- Create: `projects/warp/internal/core/cli.go`
- Create: `projects/warp/internal/core/cli_test.go`
- Modify: `projects/warp/internal/core/dispatch.go`

**Interfaces:**
- Consumes: `Credential`, `RefreshAccessToken`, `pluginapi.CommandLine*`.
- Produces: `handleCLIRegister/handleCLIExecute`; `keychainReader func() (string, error)` (injectable; default runs `security`).

- [ ] **Step 1: Write `cli.go`**

```go
package core

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// keychainReader returns the raw JSON stored under dev.warp.Warp-Stable (macOS).
var keychainReader = func() (string, error) {
	out, err := exec.Command("security", "find-generic-password",
		"-s", "dev.warp.Warp-Stable", "-w").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func handleCLIRegister(raw []byte) (json.RawMessage, error) {
	return okEnvelope(pluginapi.CommandLineRegistrationResponse{
		Flags: []pluginapi.CommandLineFlag{
			{Name: "warp-login", Usage: "Import Warp credentials from the local Keychain and save them.", Type: "bool", DefaultValue: "false"},
			{Name: "warp-refresh-token", Usage: "Provide a Warp Firebase refresh token directly instead of reading the Keychain.", Type: "string", DefaultValue: ""},
		},
	})
}

func handleCLIExecute(raw []byte) (json.RawMessage, error) {
	var req pluginapi.CommandLineExecutionRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	// Only act when our flag triggered.
	if _, ok := req.TriggeredFlags["warp-login"]; !ok {
		if _, ok2 := req.TriggeredFlags["warp-refresh-token"]; !ok2 {
			return okEnvelope(pluginapi.CommandLineExecutionResponse{})
		}
	}

	refresh := ""
	if v, ok := req.Flags["warp-refresh-token"]; ok && v.Value != "" {
		refresh = strings.TrimSpace(v.Value)
	}
	if refresh == "" {
		blob, err := keychainReader()
		if err != nil {
			return okEnvelope(pluginapi.CommandLineExecutionResponse{
				Stderr:   []byte("could not read Warp Keychain entry; pass --warp-refresh-token: " + err.Error() + "\n"),
				ExitCode: 1,
			})
		}
		refresh, err = extractRefreshToken(blob)
		if err != nil {
			return okEnvelope(pluginapi.CommandLineExecutionResponse{
				Stderr: []byte(err.Error() + "\n"), ExitCode: 1})
		}
	}

	access, exp, err := RefreshAccessToken(warpHTTPClient, refreshEndpointVar, FirebaseKey, refresh)
	if err != nil {
		return okEnvelope(pluginapi.CommandLineExecutionResponse{
			Stderr: []byte("initial token refresh failed: " + err.Error() + "\n"), ExitCode: 1})
	}
	cred := Credential{Type: "warp", RefreshToken: refresh, AccessToken: access, ExpiresAt: exp}
	auth := credentialToAuthData("warp.json", cred)
	return okEnvelope(pluginapi.CommandLineExecutionResponse{
		Stdout: []byte("Warp credential imported and verified. Saved as warp.json.\n"),
		Auths:  []pluginapi.AuthData{auth},
	})
}

// extractRefreshToken pulls refresh_token from the Warp Keychain JSON blob,
// tolerating both flat and nested {"id_token":{"refresh_token":...}} shapes.
func extractRefreshToken(blob string) (string, error) {
	var flat struct {
		RefreshToken string `json:"refresh_token"`
	}
	if json.Unmarshal([]byte(blob), &flat) == nil && flat.RefreshToken != "" {
		return flat.RefreshToken, nil
	}
	var nested struct {
		IDToken struct {
			RefreshToken string `json:"refresh_token"`
		} `json:"id_token"`
	}
	if json.Unmarshal([]byte(blob), &nested) == nil && nested.IDToken.RefreshToken != "" {
		return nested.IDToken.RefreshToken, nil
	}
	return "", fmt.Errorf("no refresh_token found in Warp Keychain payload")
}
```

- [ ] **Step 2: Wire CLI methods into `dispatch.go`**

```go
	case pluginabi.MethodCommandLineRegister:
		return handleCLIRegister(request)
	case pluginabi.MethodCommandLineExecute:
		return handleCLIExecute(request)
```

- [ ] **Step 3: Write `cli_test.go`**

```go
package core

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

func TestCLIExecute_ImportsFromKeychain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"aa.eyJleHAiOjQ4MDAwMDAwMDB9.bb"}`))
	}))
	defer srv.Close()
	warpHTTPClient = srv.Client()
	old := refreshEndpointVar
	refreshEndpointVar = srv.URL
	defer func() { refreshEndpointVar = old }()
	keychainReader = func() (string, error) { return `{"id_token":{"refresh_token":"RT-123"}}`, nil }

	req := `{"TriggeredFlags":{"warp-login":{"Name":"warp-login","Type":"bool","Value":"true","Set":true}},"Flags":{}}`
	out, err := Dispatch(pluginabi.MethodCommandLineExecute, []byte(req), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !containsRaw(out, `"Auths"`) || !containsRaw(out, `imported`) {
		t.Fatalf("expected Auths + confirmation: %s", out)
	}
}

func TestExtractRefreshToken_NestedAndFlat(t *testing.T) {
	if v, _ := extractRefreshToken(`{"refresh_token":"A"}`); v != "A" {
		t.Fatal("flat failed")
	}
	if v, _ := extractRefreshToken(`{"id_token":{"refresh_token":"B"}}`); v != "B" {
		t.Fatal("nested failed")
	}
}
```

- [ ] **Step 4: Run → PASS**

Run: `go test ./internal/core/ -run 'TestCLI|TestExtract' -v`

- [ ] **Step 5: Commit**

```bash
git add projects/warp/internal/core/{cli.go,cli_test.go,dispatch.go}
git commit -m "feat(warp): --warp-login credential import command"
```

---

### Task 6: `model_registrar`

**Files:**
- Create: `projects/warp/internal/core/models.go`
- Create: `projects/warp/internal/core/models_test.go`
- Modify: `projects/warp/internal/core/dispatch.go`

**Interfaces:**
- Consumes: `CurrentConfig()`, `pluginapi.ModelInfo`, `pluginapi.ModelRegistrationResponse`.
- Produces: `handleModelRegister`; `warpModelIDs []string` (unprefixed); `prefixedModelID(id)`, `stripModelPrefix(id)`.

- [ ] **Step 1: Write `models.go`**

```go
package core

import (
	"encoding/json"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// warpModelIDs is the curated v1 catalogue (Warp's server-side IDs).
var warpModelIDs = []string{
	"auto",
	"claude-4.1-opus", "claude-4-opus", "claude-4-sonnet", "claude-4.5-sonnet",
	"gpt-5", "gpt-5 (high reasoning)", "gpt-4.1", "gpt-4o", "o3",
	"gemini-2.5-pro",
}

func prefixedModelID(id string) string { return CurrentConfig().ModelPrefix + id }

func stripModelPrefix(id string) string {
	p := CurrentConfig().ModelPrefix
	return strings.TrimPrefix(id, p)
}

func handleModelRegister(raw []byte) (json.RawMessage, error) {
	models := make([]pluginapi.ModelInfo, 0, len(warpModelIDs))
	for _, id := range warpModelIDs {
		models = append(models, pluginapi.ModelInfo{
			ID:                         prefixedModelID(id),
			Object:                     "model",
			OwnedBy:                    "warp",
			DisplayName:                "Warp: " + id,
			SupportedGenerationMethods: []string{"chat"},
			ContextLength:              200000,
			UserDefined:                true,
		})
	}
	return okEnvelope(pluginapi.ModelRegistrationResponse{Provider: "warp", Models: models})
}
```

- [ ] **Step 2: Wire into `dispatch.go`**

```go
	case pluginabi.MethodModelRegister:
		return handleModelRegister(request)
```

- [ ] **Step 3: Write `models_test.go`, run → PASS**

```go
package core

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

func TestModelRegister_PrefixedAndOwned(t *testing.T) {
	_ = applyConfigYAML(nil) // model_prefix "warp/"
	out, err := Dispatch(pluginabi.MethodModelRegister, []byte(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !containsRaw(out, `"warp/claude-4-sonnet"`) || !containsRaw(out, `"OwnedBy":"warp"`) {
		t.Fatalf("bad model list: %s", out)
	}
}

func TestStripModelPrefix(t *testing.T) {
	_ = applyConfigYAML(nil)
	if stripModelPrefix("warp/gpt-5") != "gpt-5" {
		t.Fatal("strip failed")
	}
}
```

Run: `go test ./internal/core/ -run 'TestModel|TestStrip' -v`

- [ ] **Step 4: Commit**

```bash
git add projects/warp/internal/core/{models.go,models_test.go,dispatch.go}
git commit -m "feat(warp): model_registrar — curated prefixed catalogue"
```

---

### Task 7: chat-completions → Warp `Request` protobuf

**Files:**
- Create: `projects/warp/internal/core/chat.go`
- Create: `projects/warp/internal/core/warpreq.go`
- Create: `projects/warp/internal/core/warpreq_test.go`

**Interfaces:**
- Consumes: confirmed `warppb` accessors (Task 2), `Config`.
- Produces: `type ChatRequest`, `type ChatMessage`, `parseChatRequest([]byte) (ChatRequest, error)`; `BuildWarpRequest(cfg Config, cr ChatRequest) ([]byte, error)` (returns marshaled protobuf bytes).

- [ ] **Step 1: Write `chat.go`**

```go
package core

import "encoding/json"

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage  `json:"messages"`
	Stream   bool          `json:"stream"`
}

func parseChatRequest(raw []byte) (ChatRequest, error) {
	var cr ChatRequest
	if err := json.Unmarshal(raw, &cr); err != nil {
		return ChatRequest{}, err
	}
	return cr, nil
}
```
> `content` can be a string or an array of content parts in chat-completions. For v1 assume string; if the host may send parts, add a `json.RawMessage` content field + a flattener. Note this in `README` as a v1 limitation.

- [ ] **Step 2: Write `warpreq.go`** (uses the confirmed Task-2 accessors)

```go
package core

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"
	warppb "github.com/warpdotdev/warp-proto-apis/apis/multi_agent/v1/gen/go"
)

// BuildWarpRequest maps a chat-completions request to a marshaled Warp Request.
func BuildWarpRequest(cfg Config, cr ChatRequest) ([]byte, error) {
	if len(cr.Messages) == 0 {
		return nil, fmt.Errorf("no messages")
	}
	model := stripModelPrefix(cr.Model)
	if model == "" {
		model = "auto"
	}

	// Split: system text (folded), history (all but last), current (last).
	var systemParts []string
	var history []ChatMessage
	for _, m := range cr.Messages {
		if m.Role == "system" {
			systemParts = append(systemParts, m.Content)
		}
	}
	nonSystem := filterNonSystem(cr.Messages)
	if len(nonSystem) == 0 {
		return nil, fmt.Errorf("no user/assistant messages")
	}
	last := nonSystem[len(nonSystem)-1]
	history = nonSystem[:len(nonSystem)-1]

	// history -> task_context.tasks[0].messages[]
	taskID := "task-0"
	histMsgs := make([]*warppb.Message, 0, len(history))
	for i, m := range history {
		msg := historyMessage(taskID, fmt.Sprintf("m-%d", i), m)
		if msg != nil {
			histMsgs = append(histMsgs, msg)
		}
	}

	// current turn -> input.user_inputs.inputs[0].user_query
	uq := warppb.Request_Input_UserQuery_builder{Query: proto.String(last.Content)}
	if len(systemParts) > 0 {
		uq.ReferencedAttachments = map[string]*warppb.Attachment{
			"SYSTEM_PROMPT": warppb.Attachment_builder{
				PlainText: proto.String(strings.Join(systemParts, "\n\n")),
			}.Build(),
		}
	}

	req := warppb.Request_builder{
		TaskContext: warppb.Request_TaskContext_builder{
			Tasks: []*warppb.Task{
				warppb.Task_builder{Id: proto.String(taskID), Messages: histMsgs}.Build(),
			},
		}.Build(),
		Input: warppb.Request_Input_builder{
			UserInputs: warppb.Request_Input_UserInputs_builder{
				Inputs: []*warppb.Request_Input_UserInputs_UserInput{
					warppb.Request_Input_UserInputs_UserInput_builder{UserQuery: uq.Build()}.Build(),
				},
			}.Build(),
		}.Build(),
		Settings: warppb.Request_Settings_builder{
			ModelConfig: warppb.Request_Settings_ModelConfig_builder{Base: proto.String(model)}.Build(),
			ApiKeys:     warppb.Request_Settings_ApiKeys_builder{AllowUseOfWarpCredits: proto.Bool(cfg.UseWarpCredits)}.Build(),
		}.Build(),
	}.Build()

	return proto.Marshal(req)
}

func historyMessage(taskID, id string, m ChatMessage) *warppb.Message {
	switch m.Role {
	case "user":
		return warppb.Message_builder{
			Id: proto.String(id), TaskId: proto.String(taskID),
			UserQuery: warppb.Message_UserQuery_builder{Query: proto.String(m.Content)}.Build(),
		}.Build()
	case "assistant":
		return warppb.Message_builder{
			Id: proto.String(id), TaskId: proto.String(taskID),
			AgentOutput: warppb.Message_AgentOutput_builder{Text: proto.String(m.Content)}.Build(),
		}.Build()
	}
	return nil
}

func filterNonSystem(msgs []ChatMessage) []ChatMessage {
	out := make([]ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		if m.Role != "system" {
			out = append(out, m)
		}
	}
	return out
}
```
> If Task 2 recorded different nested type names (e.g. `Task` lives in package as `Task` vs `Request_Task`), fix these references to match. The oneof arms (`UserQuery`/`AgentOutput` on `Message`, `UserInputs` on `Input`) are set directly by the builder field in the Opaque API.

- [ ] **Step 3: Write `warpreq_test.go`**

```go
package core

import (
	"testing"

	"google.golang.org/protobuf/proto"
	warppb "github.com/warpdotdev/warp-proto-apis/apis/multi_agent/v1/gen/go"
)

func TestBuildWarpRequest_MapsFields(t *testing.T) {
	_ = applyConfigYAML(nil)
	cr := ChatRequest{
		Model: "warp/claude-4-sonnet",
		Messages: []ChatMessage{
			{Role: "system", Content: "be terse"},
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
			{Role: "user", Content: "2+2?"},
		},
	}
	raw, err := BuildWarpRequest(CurrentConfig(), cr)
	if err != nil {
		t.Fatal(err)
	}
	var req warppb.Request
	if err := proto.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	if got := req.GetSettings().GetModelConfig().GetBase(); got != "claude-4-sonnet" {
		t.Fatalf("model base = %q (prefix not stripped?)", got)
	}
	if !req.GetSettings().GetApiKeys().GetAllowUseOfWarpCredits() {
		t.Fatal("warp credits flag not set")
	}
	inputs := req.GetInput().GetUserInputs().GetInputs()
	if len(inputs) != 1 || inputs[0].GetUserQuery().GetQuery() != "2+2?" {
		t.Fatalf("current turn wrong: %+v", inputs)
	}
	tasks := req.GetTaskContext().GetTasks()
	if len(tasks) != 1 || len(tasks[0].GetMessages()) != 2 {
		t.Fatalf("history wrong: %d messages", len(tasks[0].GetMessages()))
	}
	att := inputs[0].GetUserQuery().GetReferencedAttachments()
	if att["SYSTEM_PROMPT"].GetPlainText() != "be terse" {
		t.Fatal("system prompt not folded")
	}
}
```

- [ ] **Step 4: Run → PASS**

Run: `go test ./internal/core/ -run TestBuildWarpRequest -v`

- [ ] **Step 5: Commit**

```bash
git add projects/warp/internal/core/{chat.go,warpreq.go,warpreq_test.go}
git commit -m "feat(warp): chat-completions -> Warp Request protobuf mapping"
```

---

### Task 8: Warp `ResponseEvent` (SSE) → chat-completions

**Files:**
- Create: `projects/warp/internal/core/warpresp.go`
- Create: `projects/warp/internal/core/warpresp_test.go`

**Interfaces:**
- Produces: `decodeSSELine(line string) (ev *warppb.ResponseEvent, done bool, ok bool, err error)`; `eventText(ev) string`; `finishReason(ev) (string, bool)`; `chatChunkJSON(model, delta, finish string) []byte`; `chatFullJSON(model, content, finish string) []byte`.

- [ ] **Step 1: Write `warpresp.go`**

```go
package core

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"

	"google.golang.org/protobuf/proto"
	warppb "github.com/warpdotdev/warp-proto-apis/apis/multi_agent/v1/gen/go"
)

// decodeSSELine parses one `data:` line. Returns done=true for the [DONE] sentinel.
func decodeSSELine(line string) (*warppb.ResponseEvent, bool, bool, error) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "data:") {
		return nil, false, false, nil
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" {
		return nil, false, false, nil
	}
	if payload == "[DONE]" {
		return nil, true, false, nil
	}
	raw, err := decodePayload(payload)
	if err != nil {
		return nil, false, false, err
	}
	var ev warppb.ResponseEvent
	if err := proto.Unmarshal(raw, &ev); err != nil {
		return nil, false, false, err
	}
	return &ev, false, true, nil
}

func decodePayload(s string) ([]byte, error) {
	s = strings.Trim(s, `"`)
	s = strings.Join(strings.Fields(s), "")
	// base64url with padding
	pad := (4 - len(s)%4) % 4
	if b, err := base64.URLEncoding.DecodeString(s + strings.Repeat("=", pad)); err == nil {
		return b, nil
	}
	// hex fallback
	if b, err := hex.DecodeString(s); err == nil {
		return b, nil
	}
	// std base64 fallback
	return base64.StdEncoding.DecodeString(s + strings.Repeat("=", pad))
}

// eventText extracts assistant text deltas from a ResponseEvent (empty if none).
func eventText(ev *warppb.ResponseEvent) string {
	ca := ev.GetClientActions()
	if ca == nil {
		return ""
	}
	var sb strings.Builder
	for _, a := range ca.GetActions() {
		if app := a.GetAppendToMessageContent(); app != nil {
			sb.WriteString(app.GetMessage().GetAgentOutput().GetText())
		}
		if add := a.GetAddMessagesToTask(); add != nil {
			for _, m := range add.GetMessages() {
				sb.WriteString(m.GetAgentOutput().GetText())
			}
		}
	}
	return sb.String()
}

// finishReason returns an OpenAI finish reason when the event is terminal.
func finishReason(ev *warppb.ResponseEvent) (string, bool) {
	fin := ev.GetFinished()
	if fin == nil {
		return "", false
	}
	switch {
	case fin.GetMaxTokenLimit() != nil:
		return "length", true
	default:
		return "stop", true
	}
}

// chatChunkJSON builds one chat-completions SSE data payload (without the "data: " prefix).
func chatChunkJSON(model, delta, finish string) []byte {
	choice := map[string]any{"index": 0, "delta": map[string]any{}}
	if delta != "" {
		choice["delta"] = map[string]any{"content": delta}
	}
	if finish != "" {
		choice["finish_reason"] = finish
	}
	obj := map[string]any{
		"object":  "chat.completion.chunk",
		"model":   model,
		"choices": []any{choice},
	}
	b, _ := json.Marshal(obj)
	return b
}

func chatFullJSON(model, content, finish string) []byte {
	if finish == "" {
		finish = "stop"
	}
	obj := map[string]any{
		"object": "chat.completion",
		"model":  model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": content},
			"finish_reason": finish,
		}},
	}
	b, _ := json.Marshal(obj)
	return b
}
```

- [ ] **Step 2: Write `warpresp_test.go`**

```go
package core

import (
	"encoding/base64"
	"testing"

	"google.golang.org/protobuf/proto"
	warppb "github.com/warpdotdev/warp-proto-apis/apis/multi_agent/v1/gen/go"
)

func encodeEvent(ev *warppb.ResponseEvent) string {
	raw, _ := proto.Marshal(ev)
	return "data: " + base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(raw)
}

func TestDecodeSSE_TextDelta(t *testing.T) {
	ev := warppb.ResponseEvent_builder{
		ClientActions: warppb.ResponseEvent_ClientActions_builder{
			Actions: []*warppb.ClientAction{
				warppb.ClientAction_builder{
					AppendToMessageContent: warppb.ClientAction_AppendToMessageContent_builder{
						Message: warppb.Message_builder{
							AgentOutput: warppb.Message_AgentOutput_builder{Text: proto.String("Hello")}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			},
		}.Build(),
	}.Build()

	got, done, ok, err := decodeSSELine(encodeEvent(ev))
	if err != nil || !ok || done {
		t.Fatalf("decode: err=%v ok=%v done=%v", err, ok, done)
	}
	if eventText(got) != "Hello" {
		t.Fatalf("text = %q", eventText(got))
	}
}

func TestDecodeSSE_Done(t *testing.T) {
	_, done, _, _ := decodeSSELine("data: [DONE]")
	if !done {
		t.Fatal("expected done")
	}
}

func TestFinishReason(t *testing.T) {
	ev := warppb.ResponseEvent_builder{
		Finished: warppb.ResponseEvent_StreamFinished_builder{}.Build(),
	}.Build()
	if r, ok := finishReason(ev); !ok || r != "stop" {
		t.Fatalf("finish = %q ok=%v", r, ok)
	}
}
```
> If the Task-2 spike showed different oneof setter names (e.g. `Done` on `StreamFinished`), adjust. `GetMaxTokenLimit()` returning a non-nil pointer is the Opaque-API way to test a oneof arm; confirm the getter name from the generated code.

- [ ] **Step 3: Run → PASS**

Run: `go test ./internal/core/ -run 'TestDecodeSSE|TestFinishReason' -v`

- [ ] **Step 4: Commit**

```bash
git add projects/warp/internal/core/{warpresp.go,warpresp_test.go}
git commit -m "feat(warp): Warp ResponseEvent SSE -> chat-completions decoding"
```

---

### Task 9: `executor.execute` (non-streaming)

**Files:**
- Create: `projects/warp/internal/core/executor.go`
- Create: `projects/warp/internal/core/executor_test.go`
- Modify: `projects/warp/internal/core/dispatch.go`

**Interfaces:**
- Consumes: `pluginapi.ExecutorRequest/ExecutorResponse`, `BuildWarpRequest`, `decodeSSELine`, `eventText`, `finishReason`, `chatFullJSON`, `Credential`, `warpHTTPClient`.
- Produces: `executorIdentifier()`, `handleExecute`, `postWarp(ctx, cfg, cred, body) (*http.Response, error)`, package var `warpAIEndpoint`.

- [ ] **Step 1: Write `executor.go`**

```go
package core

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

var warpAIEndpoint = "https://app.warp.dev/ai/multi-agent"

func executorIdentifier() (json.RawMessage, error) {
	return okEnvelope(map[string]string{"identifier": "warp"})
}

// postWarp sends the serialized protobuf request and returns the raw SSE response.
func postWarp(ctx context.Context, cfg Config, cred Credential, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, warpAIEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/x-protobuf")
	req.Header.Set("accept", "text/event-stream")
	req.Header.Set("authorization", "Bearer "+cred.AccessToken)
	req.Header.Set("x-warp-client-version", cfg.ClientVersion)
	req.Header.Set("x-warp-os-category", cfg.OSCategory)
	req.Header.Set("x-warp-os-name", cfg.OSName)
	req.Header.Set("x-warp-os-version", cfg.OSVersion)
	return warpHTTPClient.Do(req)
}

// quotaError maps upstream 429 / quota to a stable error.
func quotaError(status int, body string) error {
	if status == http.StatusTooManyRequests ||
		strings.Contains(body, "No remaining quota") ||
		strings.Contains(body, "No AI requests remaining") {
		return fmt.Errorf("warp_quota_exhausted: %s", strings.TrimSpace(body))
	}
	return fmt.Errorf("warp upstream status %d: %s", status, strings.TrimSpace(body))
}

func handleExecute(request []byte) (json.RawMessage, error) {
	var er pluginapi.ExecutorRequest
	if err := json.Unmarshal(request, &er); err != nil {
		return nil, err
	}
	cfg := CurrentConfig()
	var cred Credential
	if err := json.Unmarshal(er.StorageJSON, &cred); err != nil {
		return nil, fmt.Errorf("decode warp credential: %w", err)
	}
	cr, err := parseChatRequest(er.Payload)
	if err != nil {
		return nil, err
	}
	if cr.Model == "" {
		cr.Model = er.Model
	}
	body, err := BuildWarpRequest(cfg, cr)
	if err != nil {
		return nil, err
	}
	resp, err := postWarp(context.Background(), cfg, cred, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, quotaError(resp.StatusCode, string(b))
	}

	var content strings.Builder
	finish := "stop"
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		ev, done, ok, derr := decodeSSELine(sc.Text())
		if derr != nil {
			continue // skip undecodable lines defensively
		}
		if done {
			break
		}
		if !ok {
			continue
		}
		content.WriteString(eventText(ev))
		if r, term := finishReason(ev); term {
			finish = r
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	full := chatFullJSON(er.Model, content.String(), finish)
	return okEnvelope(pluginapi.ExecutorResponse{
		Payload: full,
		Headers: http.Header{"Content-Type": []string{"application/json"}},
	})
}
```

- [ ] **Step 2: Wire into `dispatch.go`**

```go
	case pluginabi.MethodExecutorIdentifier:
		return executorIdentifier()
	case pluginabi.MethodExecutorExecute:
		return handleExecute(request)
```

- [ ] **Step 3: Write `executor_test.go` (fake Warp SSE server)**

```go
package core

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/protobuf/proto"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	warppb "github.com/warpdotdev/warp-proto-apis/apis/multi_agent/v1/gen/go"
)

func sseEvent(text string) string {
	ev := warppb.ResponseEvent_builder{
		ClientActions: warppb.ResponseEvent_ClientActions_builder{
			Actions: []*warppb.ClientAction{
				warppb.ClientAction_builder{
					AppendToMessageContent: warppb.ClientAction_AppendToMessageContent_builder{
						Message: warppb.Message_builder{
							AgentOutput: warppb.Message_AgentOutput_builder{Text: proto.String(text)}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			},
		}.Build(),
	}.Build()
	raw, _ := proto.Marshal(ev)
	return "data: " + base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(raw) + "\n"
}

func TestExecute_NonStreaming(t *testing.T) {
	_ = applyConfigYAML(nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(sseEvent("2 + 2 = 4")))
		w.Write([]byte("data: [DONE]\n"))
	}))
	defer srv.Close()
	warpHTTPClient = srv.Client()
	warpAIEndpoint = srv.URL

	er := map[string]any{
		"Model":       "warp/claude-4-sonnet",
		"StorageJSON": []byte(`{"type":"warp","refresh_token":"RT","access_token":"tok"}`),
		"Payload":     []byte(`{"model":"warp/claude-4-sonnet","messages":[{"role":"user","content":"2+2?"}]}`),
	}
	raw, _ := json.Marshal(er)
	out, err := Dispatch(pluginabi.MethodExecutorExecute, raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !containsRaw(out, `2 + 2 = 4`) {
		t.Fatalf("missing content: %s", out)
	}
}
```

- [ ] **Step 4: Run → PASS**

Run: `go test ./internal/core/ -run TestExecute_NonStreaming -v`

- [ ] **Step 5: Commit**

```bash
git add projects/warp/internal/core/{executor.go,executor_test.go,dispatch.go}
git commit -m "feat(warp): executor.execute (non-streaming completion)"
```

---

### Task 10: `executor.execute_stream` (streaming)

**Files:**
- Create: `projects/warp/internal/core/stream.go`
- Create: `projects/warp/internal/core/stream_test.go`
- Modify: `projects/warp/internal/core/dispatch.go`

**Interfaces:**
- Consumes: `HostBridge`, `postWarp`, `decodeSSELine`, `eventText`, `finishReason`, `chatChunkJSON`.
- Produces: `handleExecuteStream(request []byte, host HostBridge) (json.RawMessage, error)`; `runWarpStream(ctx, cfg, cred, cr, model, streamID, host)`.

- [ ] **Step 1: Write `stream.go`**

```go
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type execStreamRequest struct {
	pluginapi.ExecutorRequest
	StreamID string `json:"stream_id"`
}

func handleExecuteStream(request []byte, host HostBridge) (json.RawMessage, error) {
	var er execStreamRequest
	if err := json.Unmarshal(request, &er); err != nil {
		return nil, err
	}
	if host == nil {
		return nil, fmt.Errorf("streaming requires host bridge")
	}
	cfg := CurrentConfig()
	var cred Credential
	if err := json.Unmarshal(er.StorageJSON, &cred); err != nil {
		return nil, err
	}
	cr, err := parseChatRequest(er.Payload)
	if err != nil {
		return nil, err
	}
	if cr.Model == "" {
		cr.Model = er.Model
	}
	streamID := er.StreamID
	model := er.Model

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				_ = host.StreamClose(streamID, fmt.Sprintf("panic: %v", rec))
			}
		}()
		if runErr := runWarpStream(context.Background(), cfg, cred, cr, model, streamID, host); runErr != nil {
			_ = host.StreamClose(streamID, runErr.Error())
			return
		}
		_ = host.StreamClose(streamID, "")
	}()

	// Return headers immediately; chunks flow via host.stream.emit.
	return okEnvelope(map[string]any{
		"headers": http.Header{"Content-Type": []string{"text/event-stream"}},
	})
}

func runWarpStream(ctx context.Context, cfg Config, cred Credential, cr ChatRequest, model, streamID string, host HostBridge) error {
	body, err := BuildWarpRequest(cfg, cr)
	if err != nil {
		return err
	}
	resp, err := postWarp(ctx, cfg, cred, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return quotaError(resp.StatusCode, string(b))
	}

	emit := func(payload []byte) error {
		return host.StreamEmit(streamID, append([]byte("data: "), append(payload, '\n', '\n')...))
	}

	sc := newSSEScanner(resp.Body)
	finish := "stop"
	for sc.Scan() {
		ev, done, ok, derr := decodeSSELine(sc.Text())
		if derr != nil {
			continue
		}
		if done {
			break
		}
		if !ok {
			continue
		}
		if txt := eventText(ev); txt != "" {
			if e := emit(chatChunkJSON(model, txt, "")); e != nil {
				return e
			}
		}
		if r, term := finishReason(ev); term {
			finish = r
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if e := emit(chatChunkJSON(model, "", finish)); e != nil {
		return e
	}
	return host.StreamEmit(streamID, []byte("data: [DONE]\n\n"))
}
```
> Add a small `newSSEScanner(r io.Reader) *bufio.Scanner` helper in `warpresp.go` (buffer 8 MiB) and reuse it in `handleExecute` too (DRY the scanner setup).

- [ ] **Step 2: Wire into `dispatch.go`**

```go
	case pluginabi.MethodExecutorExecuteStream:
		return handleExecuteStream(request, host)
	case pluginabi.MethodExecutorCountTokens:
		return handleCountTokens(request)   // Task 11
	case pluginabi.MethodExecutorHTTPRequest:
		return okEnvelope(pluginapi.ExecutorResponse{}) // v1 no-op
```

- [ ] **Step 3: Write `stream_test.go` (fake HostBridge captures emits)**

```go
package core

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	warppb "github.com/warpdotdev/warp-proto-apis/apis/multi_agent/v1/gen/go"
)

type fakeHost struct {
	mu     sync.Mutex
	chunks []string
	closed chan string
}

func newFakeHost() *fakeHost { return &fakeHost{closed: make(chan string, 1)} }
func (f *fakeHost) StreamEmit(id string, p []byte) error {
	f.mu.Lock()
	f.chunks = append(f.chunks, string(p))
	f.mu.Unlock()
	return nil
}
func (f *fakeHost) StreamClose(id, e string) error { f.closed <- e; return nil }
func (f *fakeHost) Log(string, string)             {}

func TestExecuteStream_EmitsChunks(t *testing.T) {
	_ = applyConfigYAML(nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(sseEvent("Hel")))
		w.Write([]byte(sseEvent("lo")))
		w.Write([]byte("data: [DONE]\n"))
	}))
	defer srv.Close()
	warpHTTPClient = srv.Client()
	warpAIEndpoint = srv.URL

	host := newFakeHost()
	er := map[string]any{
		"Model":       "warp/claude-4-sonnet",
		"StorageJSON": []byte(`{"type":"warp","access_token":"tok"}`),
		"Payload":     []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
		"stream_id":   "s-1",
	}
	raw, _ := json.Marshal(er)
	if _, err := Dispatch(pluginabi.MethodExecutorExecuteStream, raw, host); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-host.closed:
		if e != "" {
			t.Fatalf("stream closed with error: %s", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not close")
	}
	joined := strings.Join(host.chunks, "")
	if !strings.Contains(joined, "Hel") || !strings.Contains(joined, "lo") || !strings.Contains(joined, "[DONE]") {
		t.Fatalf("chunks missing content: %q", joined)
	}
	_ = base64.URLEncoding
	_ = proto.Marshal
	_ = warppb.Request{}
}
```

- [ ] **Step 4: Run → PASS**

Run: `go test ./internal/core/ -run TestExecuteStream -v`

- [ ] **Step 5: Commit**

```bash
git add projects/warp/internal/core/{stream.go,stream_test.go,dispatch.go,warpresp.go}
git commit -m "feat(warp): executor.execute_stream (live SSE via host.stream.emit)"
```

---

### Task 11: count_tokens, final wiring, build, and live host verification

**Files:**
- Modify: `projects/warp/internal/core/executor.go` (add `handleCountTokens`)
- Modify: `projects/warp/internal/core/executor_test.go`
- Modify: `projects/warp/README.md`

**Interfaces:**
- Produces: `handleCountTokens(request []byte) (json.RawMessage, error)` — rough estimate (chars/4).

- [ ] **Step 1: Write `handleCountTokens` (estimate)**

```go
func handleCountTokens(request []byte) (json.RawMessage, error) {
	var er pluginapi.ExecutorRequest
	if err := json.Unmarshal(request, &er); err != nil {
		return nil, err
	}
	cr, err := parseChatRequest(er.Payload)
	if err != nil {
		return okEnvelope(pluginapi.ExecutorResponse{Payload: []byte(`{"input_tokens":0}`)})
	}
	total := 0
	for _, m := range cr.Messages {
		total += len(m.Content)/4 + 1
	}
	body, _ := json.Marshal(map[string]int{"input_tokens": total})
	return okEnvelope(pluginapi.ExecutorResponse{Payload: body})
}
```

- [ ] **Step 2: Test it, run → PASS**

```go
func TestCountTokens_Estimates(t *testing.T) {
	er := map[string]any{"Payload": []byte(`{"messages":[{"role":"user","content":"abcdefgh"}]}`)}
	raw, _ := json.Marshal(er)
	out, err := Dispatch(pluginabi.MethodExecutorCountTokens, raw, nil)
	if err != nil || !containsRaw(out, `"input_tokens"`) {
		t.Fatalf("count err=%v out=%s", err, out)
	}
}
```

Run: `go test ./internal/core/... -v` → all PASS.

- [ ] **Step 3: Full build + ABI check**

Run: `cd projects/warp && make build && nm -gU warp.dylib | grep cliproxy_plugin_init`
Expected: builds; export present.

- [ ] **Step 4: Live host verification** (uses the saved "CLIProxyAPI local integration test" memory)

1. Build/run a CLIProxyAPI host; copy `warp.dylib` into its `plugins/darwin/arm64/` (or `plugins/`).
2. Merge `config.snippet.yaml` into the host `config.yaml`; start the host.
3. `cli-proxy-api --warp-login` (with Warp app logged in) → confirm `warp.json` written to the auth dir.
4. `GET /v0/management/plugins` → `warp` present with `auth_provider, executor, model_registrar, command_line_plugin`.
5. `GET /v1/models` → shows `warp/claude-4-sonnet` etc.
6. Non-stream: `curl /v1/chat/completions -d '{"model":"warp/claude-4-sonnet","messages":[{"role":"user","content":"say hi"}]}'` → a real reply.
7. Stream: same with `"stream":true` → incremental chunks.
8. Confirm Warp credits decremented in the Warp dashboard.

Record actual results (pass/fail + any error output) in the commit message; do not claim success without the curl output.

- [ ] **Step 5: Finalize README + commit**

Add to `README.md`: config keys, `--warp-login` usage, the client-version-staleness note, the v1 limitations (no tools/images; string content only), and the AGPL + unofficial-integration notice.

```bash
git add projects/warp
git commit -m "feat(warp): count_tokens + finalize; verified against live CLIProxyAPI host"
```

---

## Self-Review

**Spec coverage:**
- §4 auth-file credential → Tasks 3–4 ✓; `--warp-login` §8 → Task 5 ✓; `auth.refresh` §6.3 → Task 4 ✓.
- §5 four capabilities → Task 1 registration ✓ (executor formats/scope ✓).
- §7 request mapping (credits flag, history split, system fold) → Task 7 ✓; response decode → Task 8 ✓; execute → Task 9 ✓; execute_stream via host.stream.emit → Task 10 ✓; transport headers → Task 9 `postWarp` ✓.
- §9 models + `warp/` prefix → Task 6 ✓.
- §10 config schema → Task 1 `config.go` ✓.
- §11 quota/error mapping → Task 9 `quotaError` ✓ (finish-reason mapping Task 8 ✓).
- §12 build/verify → Task 11 ✓.
- §13 phase order → Tasks 1–11 mirror the phases ✓.
- Protobuf Opaque-API risk (§15) → Task 2 spike de-risks and pins names ✓.

**Placeholder scan:** The two `itoa`/inline notes in Task 3 Step 2 and the `content`-as-array note in Task 7 Step 1 are explicit "replace with X" instructions with the exact replacement given, not open TODOs. All protobuf accessor names are anchored to the Task 2 spike, which instructs recording the real names. No "add error handling"/"TBD" left.

**Type consistency:** `HostBridge` (StreamEmit/StreamClose/Log) consistent across Tasks 1, 10. `Credential`, `RefreshAccessToken`, `warpHTTPClient`, `refreshEndpointVar`, `warpAIEndpoint`, `prefixedModelID`/`stripModelPrefix`, `BuildWarpRequest`, `decodeSSELine`/`eventText`/`finishReason`/`chatChunkJSON`/`chatFullJSON` names match between definition and use. `okEnvelope`/`Dispatch` signatures stable. One follow-up baked into Task 10 note: DRY the SSE scanner (`newSSEScanner`) and use it in Task 9 too.

**Known verification points (call out during execution, not blockers):** exact `pluginabi.Envelope` JSON shape (Task 1 note); exact generated protobuf type/getter names (Task 2); `pluginapi.AuthParseResponse`/`AuthLogin*` field names (Task 4 note). Each has an inline "confirm against references/upstream" instruction.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-07-19-warp-provider-plugin.md`.
