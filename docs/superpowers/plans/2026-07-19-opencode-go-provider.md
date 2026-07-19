# OpenCode Go Provider Plugin — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `opencode-go`, a native CLIProxyAPI plugin that adds OpenCode Go (`https://opencode.ai/zen/go/v1`) as a first-class provider whose 22 models are discovered live and served through the host's built-in OpenAI-compatible executor.

**Architecture:** Two capabilities only — `auth_provider` (mint a credential carrying `Attributes{base_url, api_key}`) + `model_provider` (static catalogue + live `GET /models` discovery). **No executor:** the host's built-in `OpenAICompatExecutor` auto-binds to the `opencode-go` provider key and does the Bearer `POST /chat/completions`. All request/response logic (dispatch, auth, models) lives in a cgo-free `internal/core` package that is unit-tested with plain `go test`; a thin cgo `main.go` adapter forwards ABI calls to `core.Dispatch` and provides the `host.http.do` bridge.

**Tech Stack:** Go 1.26 + CGO (Apple clang) c-shared build; `github.com/router-for-me/CLIProxyAPI/v7` v7.2.88 SDK (`sdk/pluginapi`, `sdk/pluginabi`); stdlib only otherwise.

## Global Constraints

- Go **1.26.0**; **CGO_ENABLED=1** for the c-shared build (Apple clang). `go env GO111MODULE` on.
- SDK pin: `require github.com/router-for-me/CLIProxyAPI/v7 v7.2.88` (no `replace`).
- ABI/schema versions: `pluginabi.ABIVersion == 1`, `pluginabi.SchemaVersion == 1`.
- Provider key is the exact string **`opencode-go`** everywhere (metadata `Name`, `auth.identifier`, `AuthData.Provider`, `ModelResponse.Provider`). Host lowercases provider keys — keep it lowercase.
- Base URL is exactly **`https://opencode.ai/zen/go/v1`** (no trailing slash, no `/chat/completions` suffix — the executor appends the path).
- Auth header is `Authorization: Bearer <api_key>` (the built-in executor sets this from `Attributes["api_key"]`).
- Model IDs are copied verbatim from the verified `GET /models` catalogue (see Task 3). 22 ids.
- **Never commit a real API key.** Credential files and the real key stay out of git; only `opencode-go.json.example` (placeholder) is committed. Real key is supplied at test time via `$OPENCODE_API_KEY`.
- Wire types: marshal/unmarshal the SDK's own `pluginapi.*` structs directly so JSON field names match the host on both directions (they have no json tags → capitalized keys like `"Provider"`, `"StorageJSON"`, `"Attributes"`). The `{ok,result,error}` envelope wrapper uses lowercase tags.
- Work happens on branch `feat/opencode-go-plugin`. Commit after each task.

---

## File Structure

```
projects/opencode-go/
  go.mod                                # module + SDK require
  main.go                               # cgo adapter: C preamble + host bridge, exports, callHost/hostHTTPDo, forwards to core.Dispatch
  internal/core/dispatch.go             # envelope, registration, Dispatch router, shared consts + HTTPDoer type
  internal/core/auth.go                 # auth_provider: identifier, parse, refresh, login stubs
  internal/core/models.go               # model_provider: catalogue, static models, live discovery
  internal/core/dispatch_test.go        # register + unknown-method + decodeEnvelope helper
  internal/core/auth_test.go            # parse (handled/foreign/missing-key), identifier, login-unsupported
  internal/core/models_test.go          # static catalogue, live discovery, static fallback
  Makefile                              # build/test/tidy/clean
  README.md                             # install / wire / credential / test
  config.snippet.yaml                   # plugins.configs.opencode-go block
  opencode-go.json.example              # credential-file template (placeholder key)
CLAUDE.md                               # (repo root) document the projects/ convention
```

Design boundary: `internal/core` is pure Go (imports only `pluginapi`/`pluginabi` + stdlib) so it unit-tests without cgo. `main.go` holds all `import "C"` code and is exercised only by the build + the end-to-end host test (Task 7).

---

### Task 1: Module + core skeleton + registration

**Files:**
- Create: `projects/opencode-go/go.mod`
- Create: `projects/opencode-go/internal/core/dispatch.go`
- Test: `projects/opencode-go/internal/core/dispatch_test.go`

**Interfaces:**
- Produces: `core.ProviderKey` (`"opencode-go"`), `core.DefaultBaseURL`, `core.HTTPDoer` (`func(pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error)`), `core.Dispatch(method string, request []byte, do HTTPDoer) ([]byte, error)`, and unexported `okEnvelope`/`errorEnvelope`/`decodeEnvelope`(test).

- [ ] **Step 1: Confirm no root Go module conflicts**

Run: `test -f /Users/serkan/cpa-plugins/go.mod && echo ROOT_MODULE_EXISTS || echo no-root-module`
Expected: `no-root-module` (the plugin is a standalone nested module). If `ROOT_MODULE_EXISTS`, stop and reconsider nesting.

- [ ] **Step 2: Create `go.mod`**

`projects/opencode-go/go.mod`:
```
module github.com/pragmaticgrowth/cpa-plugins/projects/opencode-go

go 1.26.0

require github.com/router-for-me/CLIProxyAPI/v7 v7.2.88
```

- [ ] **Step 3: Write the failing test**

`projects/opencode-go/internal/core/dispatch_test.go`:
```go
package core

import (
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

// decodeEnvelope unwraps the {ok,result,error} envelope for assertions.
func decodeEnvelope(t *testing.T, raw []byte) (bool, json.RawMessage) {
	t.Helper()
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("bad envelope %q: %v", raw, err)
	}
	return env.OK, env.Result
}

func TestDispatchRegisterDeclaresCapabilities(t *testing.T) {
	raw, err := Dispatch(pluginabi.MethodPluginRegister, nil, nil)
	if err != nil {
		t.Fatalf("Dispatch register: %v", err)
	}
	ok, result := decodeEnvelope(t, raw)
	if !ok {
		t.Fatalf("register envelope not ok")
	}
	var reg struct {
		SchemaVersion uint32 `json:"schema_version"`
		Metadata      struct {
			Name string `json:"Name"`
		} `json:"metadata"`
		Capabilities struct {
			AuthProvider  bool `json:"auth_provider"`
			ModelProvider bool `json:"model_provider"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(result, &reg); err != nil {
		t.Fatalf("decode registration: %v", err)
	}
	if reg.SchemaVersion != pluginabi.SchemaVersion {
		t.Errorf("schema_version = %d, want %d", reg.SchemaVersion, pluginabi.SchemaVersion)
	}
	if reg.Metadata.Name != ProviderKey {
		t.Errorf("Name = %q, want %q", reg.Metadata.Name, ProviderKey)
	}
	if !reg.Capabilities.AuthProvider || !reg.Capabilities.ModelProvider {
		t.Errorf("capabilities = %+v, want auth+model true", reg.Capabilities)
	}
}

func TestDispatchUnknownMethod(t *testing.T) {
	raw, err := Dispatch("bogus.method", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok, _ := decodeEnvelope(t, raw); ok {
		t.Errorf("unknown method should return ok=false")
	}
}
```

- [ ] **Step 4: Run test to verify it fails**

Run: `cd projects/opencode-go && go mod tidy && go test ./internal/core/`
Expected: FAIL — `undefined: Dispatch` / `undefined: ProviderKey` (compile error).

- [ ] **Step 5: Write minimal implementation**

`projects/opencode-go/internal/core/dispatch.go`:
```go
// Package core holds the OpenCode Go plugin's ABI-method logic, free of any
// cgo. The cgo main.go adapter forwards raw method calls to Dispatch.
package core

import (
	"encoding/json"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// ProviderKey is the CLIProxyAPI provider key for OpenCode Go. It must match
// across auth.identifier, AuthData.Provider, and ModelResponse.Provider so the
// host binds discovered models to this credential + the built-in executor.
const ProviderKey = "opencode-go"

// DefaultBaseURL is the OpenCode Go gateway base URL (no path suffix; the
// built-in OpenAI-compatible executor appends /chat/completions).
const DefaultBaseURL = "https://opencode.ai/zen/go/v1"

// HTTPDoer performs an HTTP request through the host transport bridge. main.go
// supplies a real implementation backed by host.http.do; tests supply a fake.
type HTTPDoer func(pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error)

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type registration struct {
	SchemaVersion uint32                   `json:"schema_version"`
	Metadata      pluginapi.Metadata       `json:"metadata"`
	Capabilities  registrationCapabilities `json:"capabilities"`
}

type registrationCapabilities struct {
	AuthProvider  bool `json:"auth_provider"`
	ModelProvider bool `json:"model_provider"`
}

// Dispatch routes an ABI method to its handler and returns raw envelope bytes.
// do is used only by model.for_auth.
func Dispatch(method string, request []byte, do HTTPDoer) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		return okEnvelope(pluginRegistration())
	case pluginabi.MethodAuthIdentifier:
		return okEnvelope(identifierResponse{Identifier: ProviderKey})
	case pluginabi.MethodAuthParse:
		return authParse(request)
	case pluginabi.MethodAuthRefresh:
		return authRefresh(request)
	case pluginabi.MethodAuthLoginStart, pluginabi.MethodAuthLoginPoll:
		return authLoginUnsupported()
	case pluginabi.MethodModelStatic:
		return okEnvelope(staticModels())
	case pluginabi.MethodModelForAuth:
		return modelsForAuth(request, do)
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

func pluginRegistration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             ProviderKey,
			Version:          "0.1.0",
			Author:           "pragmaticgrowth",
			GitHubRepository: "https://github.com/pragmaticgrowth/cpa-plugins",
			ConfigFields:     []pluginapi.ConfigField{},
		},
		Capabilities: registrationCapabilities{AuthProvider: true, ModelProvider: true},
	}
}

func okEnvelope(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{OK: true, Result: raw})
}

func errorEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
	return raw
}
```

Note: this file references `identifierResponse`, `authParse`, `authRefresh`, `authLoginUnsupported`, `staticModels`, `modelsForAuth` — defined in Tasks 2–4. To compile Task 1 in isolation, temporarily stub them OR implement Tasks 1–4 before first `go test`. **Recommended:** create `auth.go` and `models.go` in the same commit as skeletons returning `errorEnvelope("unimplemented","")`, then flesh them out in Tasks 2–4. If using subagent-driven execution, collapse Tasks 1–4 file creation so the package always compiles; the tests remain per-task.

- [ ] **Step 6: Run test to verify it passes**

Run: `cd projects/opencode-go && go test ./internal/core/ -run TestDispatch -v`
Expected: PASS (both TestDispatch* tests).

- [ ] **Step 7: Commit**

```bash
git add projects/opencode-go/go.mod projects/opencode-go/go.sum projects/opencode-go/internal/core/dispatch.go projects/opencode-go/internal/core/dispatch_test.go
git commit -m "feat(opencode-go): core dispatch + registration"
```

---

### Task 2: `auth_provider`

**Files:**
- Create: `projects/opencode-go/internal/core/auth.go`
- Test: `projects/opencode-go/internal/core/auth_test.go`

**Interfaces:**
- Consumes: `ProviderKey`, `DefaultBaseURL`, `okEnvelope`, `errorEnvelope` (Task 1).
- Produces: `identifierResponse`, `authParse`, `authRefresh`, `authLoginUnsupported`, `buildAuth`, `credentialFile`.

- [ ] **Step 1: Write the failing tests**

`projects/opencode-go/internal/core/auth_test.go`:
```go
package core

import (
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestAuthIdentifier(t *testing.T) {
	raw, _ := Dispatch(pluginabi.MethodAuthIdentifier, nil, nil)
	ok, result := decodeEnvelope(t, raw)
	if !ok {
		t.Fatal("identifier not ok")
	}
	var got struct {
		Identifier string `json:"identifier"`
	}
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatal(err)
	}
	if got.Identifier != ProviderKey {
		t.Errorf("identifier = %q, want %q", got.Identifier, ProviderKey)
	}
}

func TestAuthParseRecognizesCredential(t *testing.T) {
	file := []byte(`{"type":"opencode-go","api_key":"sk-test123"}`)
	req, _ := json.Marshal(pluginapi.AuthParseRequest{FileName: "opencode-go.json", RawJSON: file})
	raw, err := Dispatch(pluginabi.MethodAuthParse, req, nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ok, result := decodeEnvelope(t, raw)
	if !ok {
		t.Fatal("parse not ok")
	}
	var resp pluginapi.AuthParseResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Handled {
		t.Fatal("expected Handled=true")
	}
	if resp.Auth.Provider != ProviderKey {
		t.Errorf("Provider = %q, want %q", resp.Auth.Provider, ProviderKey)
	}
	if resp.Auth.Attributes["api_key"] != "sk-test123" {
		t.Errorf("api_key attr = %q", resp.Auth.Attributes["api_key"])
	}
	if resp.Auth.Attributes["base_url"] != DefaultBaseURL {
		t.Errorf("base_url attr = %q, want %q", resp.Auth.Attributes["base_url"], DefaultBaseURL)
	}
}

func TestAuthParseIgnoresForeignCredential(t *testing.T) {
	file := []byte(`{"type":"anthropic","api_key":"sk-ant-x"}`)
	req, _ := json.Marshal(pluginapi.AuthParseRequest{RawJSON: file})
	raw, _ := Dispatch(pluginabi.MethodAuthParse, req, nil)
	ok, result := decodeEnvelope(t, raw)
	if !ok {
		t.Fatal("should still be an ok envelope")
	}
	var resp pluginapi.AuthParseResponse
	_ = json.Unmarshal(result, &resp)
	if resp.Handled {
		t.Error("must not handle a foreign credential file")
	}
}

func TestAuthParseRejectsMissingKey(t *testing.T) {
	file := []byte(`{"type":"opencode-go"}`)
	req, _ := json.Marshal(pluginapi.AuthParseRequest{RawJSON: file})
	raw, _ := Dispatch(pluginabi.MethodAuthParse, req, nil)
	if ok, _ := decodeEnvelope(t, raw); ok {
		t.Error("missing api_key should return ok=false")
	}
}

func TestAuthLoginUnsupported(t *testing.T) {
	for _, m := range []string{pluginabi.MethodAuthLoginStart, pluginabi.MethodAuthLoginPoll} {
		raw, _ := Dispatch(m, nil, nil)
		if ok, _ := decodeEnvelope(t, raw); ok {
			t.Errorf("%s should be unsupported (ok=false)", m)
		}
	}
}

func TestAuthRefreshIsNoOp(t *testing.T) {
	req, _ := json.Marshal(pluginapi.AuthRefreshRequest{
		AuthID:       "opencode-go",
		AuthProvider: ProviderKey,
		Attributes:   map[string]string{"api_key": "sk-x", "base_url": DefaultBaseURL},
	})
	raw, err := Dispatch(pluginabi.MethodAuthRefresh, req, nil)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	ok, result := decodeEnvelope(t, raw)
	if !ok {
		t.Fatal("refresh not ok")
	}
	var resp pluginapi.AuthRefreshResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Auth.Attributes["api_key"] != "sk-x" {
		t.Errorf("refresh dropped api_key: %q", resp.Auth.Attributes["api_key"])
	}
	if !resp.NextRefreshAfter.After(resp.Auth.NextRefreshAfter.AddDate(-1, 0, 0)) {
		t.Error("NextRefreshAfter should be set far in the future")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd projects/opencode-go && go test ./internal/core/ -run TestAuth`
Expected: FAIL — `undefined: authParse` etc. (or unimplemented stubs returning ok=false where handled expected).

- [ ] **Step 3: Write the implementation**

`projects/opencode-go/internal/core/auth.go`:
```go
package core

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// noRefresh marks a credential that never needs refreshing (pre-shared key).
var noRefresh = time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC)

type identifierResponse struct {
	Identifier string `json:"identifier"`
}

// credentialFile is the on-disk shape of <AuthDir>/opencode-go.json.
type credentialFile struct {
	Type    string `json:"type"`
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url,omitempty"`
}

func authParse(request []byte) ([]byte, error) {
	var req pluginapi.AuthParseRequest
	if len(request) > 0 {
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
	}
	var cred credentialFile
	if len(req.RawJSON) > 0 {
		// Non-JSON or unrelated file → empty cred → not handled below.
		_ = json.Unmarshal(req.RawJSON, &cred)
	}
	if strings.TrimSpace(cred.Type) != ProviderKey {
		return okEnvelope(pluginapi.AuthParseResponse{Handled: false})
	}
	if strings.TrimSpace(cred.APIKey) == "" {
		return errorEnvelope("invalid_credential", "opencode-go.json is missing api_key"), nil
	}
	return okEnvelope(pluginapi.AuthParseResponse{Handled: true, Auth: buildAuth(cred, req.RawJSON)})
}

func buildAuth(cred credentialFile, storage []byte) pluginapi.AuthData {
	baseURL := strings.TrimSpace(cred.BaseURL)
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return pluginapi.AuthData{
		Provider:    ProviderKey,
		ID:          ProviderKey,
		FileName:    "opencode-go.json",
		Label:       "OpenCode Go",
		StorageJSON: storage,
		Metadata:    map[string]any{"type": ProviderKey},
		Attributes: map[string]string{
			"base_url": baseURL,
			"api_key":  strings.TrimSpace(cred.APIKey),
		},
		NextRefreshAfter: noRefresh,
	}
}

func authRefresh(request []byte) ([]byte, error) {
	var req pluginapi.AuthRefreshRequest
	if len(request) > 0 {
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
	}
	id := strings.TrimSpace(req.AuthID)
	if id == "" {
		id = ProviderKey
	}
	// Pre-shared key: nothing to refresh. Echo the record back and push the
	// next refresh far out so the host stops polling us.
	auth := pluginapi.AuthData{
		Provider:         ProviderKey,
		ID:               id,
		FileName:         "opencode-go.json",
		Label:            "OpenCode Go",
		StorageJSON:      req.StorageJSON,
		Metadata:         req.Metadata,
		Attributes:       req.Attributes,
		NextRefreshAfter: noRefresh,
	}
	return okEnvelope(pluginapi.AuthRefreshResponse{Auth: auth, NextRefreshAfter: noRefresh})
}

func authLoginUnsupported() ([]byte, error) {
	return errorEnvelope("login_unsupported",
		"OpenCode Go uses a pre-shared API key; create <AuthDir>/opencode-go.json instead of an interactive login"), nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd projects/opencode-go && go test ./internal/core/ -run TestAuth -v`
Expected: PASS (all TestAuth* tests).

- [ ] **Step 5: Commit**

```bash
git add projects/opencode-go/internal/core/auth.go projects/opencode-go/internal/core/auth_test.go
git commit -m "feat(opencode-go): auth_provider parse/refresh/login-stub"
```

---

### Task 3: `model_provider` — static catalogue

**Files:**
- Create: `projects/opencode-go/internal/core/models.go`
- Test: `projects/opencode-go/internal/core/models_test.go`

**Interfaces:**
- Consumes: `ProviderKey`, `DefaultBaseURL`, `okEnvelope` (Task 1); `HTTPDoer` (Task 1, used in Task 4).
- Produces: `catalog []string`, `modelInfo(id)`, `staticModels()`, and (Task 4) `modelsForAuth`, `discoverModels`, `openaiModelList`.

- [ ] **Step 1: Write the failing test**

`projects/opencode-go/internal/core/models_test.go`:
```go
package core

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestStaticModelsCatalog(t *testing.T) {
	raw, _ := Dispatch(pluginabi.MethodModelStatic, nil, nil)
	ok, result := decodeEnvelope(t, raw)
	if !ok {
		t.Fatal("static models not ok")
	}
	var resp pluginapi.ModelResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Provider != ProviderKey {
		t.Errorf("Provider = %q, want %q", resp.Provider, ProviderKey)
	}
	if len(resp.Models) != len(catalog) {
		t.Fatalf("got %d models, want %d", len(resp.Models), len(catalog))
	}
	found := false
	for _, m := range resp.Models {
		if m.ID == "kimi-k3" {
			found = true
			if m.OwnedBy != ProviderKey {
				t.Errorf("kimi-k3 OwnedBy = %q", m.OwnedBy)
			}
			if len(m.SupportedGenerationMethods) == 0 || m.SupportedGenerationMethods[0] != "chat" {
				t.Errorf("kimi-k3 gen methods = %v", m.SupportedGenerationMethods)
			}
		}
	}
	if !found {
		t.Error("kimi-k3 missing from static catalogue")
	}
}

// Task 4 tests:

func TestModelsForAuthLiveDiscovery(t *testing.T) {
	fake := func(req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		if req.URL != DefaultBaseURL+"/models" {
			t.Errorf("URL = %q", req.URL)
		}
		if got := req.Headers.Get("Authorization"); got != "Bearer sk-live" {
			t.Errorf("Authorization = %q", got)
		}
		body := []byte(`{"object":"list","data":[{"id":"grok-4.5"},{"id":"kimi-k3"}]}`)
		return pluginapi.HTTPResponse{StatusCode: 200, Body: body}, nil
	}
	req, _ := json.Marshal(pluginapi.AuthModelRequest{
		AuthProvider: ProviderKey,
		Attributes:   map[string]string{"api_key": "sk-live", "base_url": DefaultBaseURL},
	})
	raw, err := Dispatch(pluginabi.MethodModelForAuth, req, fake)
	if err != nil {
		t.Fatalf("for_auth: %v", err)
	}
	ok, result := decodeEnvelope(t, raw)
	if !ok {
		t.Fatal("for_auth not ok")
	}
	var resp pluginapi.ModelResponse
	_ = json.Unmarshal(result, &resp)
	if resp.Provider != ProviderKey {
		t.Errorf("Provider = %q", resp.Provider)
	}
	if len(resp.Models) != 2 || resp.Models[0].ID != "grok-4.5" {
		t.Fatalf("live models = %+v, want [grok-4.5 kimi-k3]", resp.Models)
	}
}

func TestModelsForAuthFallsBackToStatic(t *testing.T) {
	fail := func(pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		return pluginapi.HTTPResponse{}, fmt.Errorf("network down")
	}
	req, _ := json.Marshal(pluginapi.AuthModelRequest{Attributes: map[string]string{"api_key": "sk-x"}})
	raw, _ := Dispatch(pluginabi.MethodModelForAuth, req, fail)
	ok, result := decodeEnvelope(t, raw)
	if !ok {
		t.Fatal("fallback not ok")
	}
	var resp pluginapi.ModelResponse
	_ = json.Unmarshal(result, &resp)
	if len(resp.Models) != len(catalog) {
		t.Errorf("fallback returned %d models, want static %d", len(resp.Models), len(catalog))
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd projects/opencode-go && go test ./internal/core/ -run TestStaticModelsCatalog`
Expected: FAIL — `undefined: catalog` / `undefined: staticModels`.

- [ ] **Step 3: Write the static-catalogue implementation**

`projects/opencode-go/internal/core/models.go` (Task 3 portion — the live-discovery funcs are added in Task 4):
```go
package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// catalog is the baked-in fallback list of OpenCode Go model ids, verified live
// on 2026-07-19 from GET https://opencode.ai/zen/go/v1/models (22 ids). Used by
// model.static and as the model.for_auth fallback when live discovery fails.
var catalog = []string{
	"grok-4.5",
	"glm-5.2", "glm-5.1", "glm-5",
	"kimi-k3", "kimi-k2.7-code", "kimi-k2.6", "kimi-k2.5",
	"deepseek-v4-pro", "deepseek-v4-flash",
	"minimax-m3", "minimax-m2.7", "minimax-m2.5",
	"qwen3.7-max", "qwen3.7-plus", "qwen3.6-plus", "qwen3.5-plus",
	"mimo-v2-pro", "mimo-v2-omni", "mimo-v2.5-pro", "mimo-v2.5",
	"hy3-preview",
}

func modelInfo(id string) pluginapi.ModelInfo {
	return pluginapi.ModelInfo{
		ID:                         id,
		Object:                     "model",
		OwnedBy:                    ProviderKey,
		DisplayName:                id,
		SupportedGenerationMethods: []string{"chat"},
		UserDefined:                true,
	}
}

func staticModels() pluginapi.ModelResponse {
	models := make([]pluginapi.ModelInfo, 0, len(catalog))
	for _, id := range catalog {
		models = append(models, modelInfo(id))
	}
	return pluginapi.ModelResponse{Provider: ProviderKey, Models: models}
}
```

Note: `encoding/json`, `fmt`, `net/http`, `strings` imports are consumed by the Task 4 additions to this same file; if implementing Task 3 alone, import only what Task 3 uses (`pluginapi`) and add the rest in Task 4 to keep the build green.

- [ ] **Step 4: Run to verify it passes**

Run: `cd projects/opencode-go && go test ./internal/core/ -run TestStaticModelsCatalog -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add projects/opencode-go/internal/core/models.go projects/opencode-go/internal/core/models_test.go
git commit -m "feat(opencode-go): model_provider static catalogue"
```

---

### Task 4: `model_provider` — live discovery + fallback

**Files:**
- Modify: `projects/opencode-go/internal/core/models.go` (add discovery)
- Test: reuse `models_test.go` `TestModelsForAuthLiveDiscovery`, `TestModelsForAuthFallsBackToStatic` (already written in Task 3).

**Interfaces:**
- Consumes: `staticModels`, `modelInfo`, `ProviderKey`, `DefaultBaseURL`, `HTTPDoer`.
- Produces: `modelsForAuth(request []byte, do HTTPDoer) ([]byte, error)`, `discoverModels`, `openaiModelList`.

- [ ] **Step 1: Confirm the tests fail**

Run: `cd projects/opencode-go && go test ./internal/core/ -run TestModelsForAuth`
Expected: FAIL — `undefined: modelsForAuth`.

- [ ] **Step 2: Add the discovery implementation to `models.go`**

Append to `projects/opencode-go/internal/core/models.go`:
```go
// openaiModelList is the shape of GET /models (OpenAI list).
type openaiModelList struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func modelsForAuth(request []byte, do HTTPDoer) ([]byte, error) {
	var req pluginapi.AuthModelRequest
	if len(request) > 0 {
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, fmt.Errorf("decode model.for_auth request: %w", err)
		}
	}
	resp, err := discoverModels(req, do)
	if err != nil {
		// Resilient fallback: still expose the baked-in catalogue.
		resp = staticModels()
	}
	return okEnvelope(resp)
}

func discoverModels(req pluginapi.AuthModelRequest, do HTTPDoer) (pluginapi.ModelResponse, error) {
	if do == nil {
		return pluginapi.ModelResponse{}, fmt.Errorf("no host HTTP bridge")
	}
	apiKey := strings.TrimSpace(req.Attributes["api_key"])
	if apiKey == "" {
		return pluginapi.ModelResponse{}, fmt.Errorf("missing api_key attribute")
	}
	baseURL := strings.TrimSpace(req.Attributes["base_url"])
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	httpResp, err := do(pluginapi.HTTPRequest{
		Method:  "GET",
		URL:     strings.TrimRight(baseURL, "/") + "/models",
		Headers: http.Header{"Authorization": {"Bearer " + apiKey}},
	})
	if err != nil {
		return pluginapi.ModelResponse{}, err
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return pluginapi.ModelResponse{}, fmt.Errorf("GET /models returned status %d", httpResp.StatusCode)
	}
	var list openaiModelList
	if err := json.Unmarshal(httpResp.Body, &list); err != nil {
		return pluginapi.ModelResponse{}, fmt.Errorf("decode /models body: %w", err)
	}
	models := make([]pluginapi.ModelInfo, 0, len(list.Data))
	for _, m := range list.Data {
		if strings.TrimSpace(m.ID) == "" {
			continue
		}
		models = append(models, modelInfo(m.ID))
	}
	if len(models) == 0 {
		return pluginapi.ModelResponse{}, fmt.Errorf("/models returned no usable ids")
	}
	return pluginapi.ModelResponse{Provider: ProviderKey, Models: models}, nil
}
```

- [ ] **Step 3: Run to verify all core tests pass**

Run: `cd projects/opencode-go && go test ./internal/core/ -v`
Expected: PASS — every TestDispatch*, TestAuth*, TestStaticModelsCatalog, TestModelsForAuth*.

- [ ] **Step 4: Commit**

```bash
git add projects/opencode-go/internal/core/models.go
git commit -m "feat(opencode-go): live model discovery with static fallback"
```

---

### Task 5: cgo adapter + c-shared build

**Files:**
- Create: `projects/opencode-go/main.go`
- Create: `projects/opencode-go/Makefile`

**Interfaces:**
- Consumes: `core.Dispatch`, `core.HTTPDoer`, `pluginabi.ABIVersion`, `pluginabi.MethodHostHTTPDo`, `pluginapi.HTTPRequest/HTTPResponse`.
- Produces: the loadable `opencode-go.<ext>` with exported `cliproxy_plugin_init`.

- [ ] **Step 1: Write `main.go`**

`projects/opencode-go/main.go`:
```go
// Command opencode-go is a CLIProxyAPI native plugin (c-shared) that adds the
// OpenCode Go provider. It is a thin ABI adapter over internal/core plus the
// host.http.do bridge; all logic lives in internal/core.
package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);

static const cliproxy_host_api* stored_host;

static void store_host_api(const cliproxy_host_api* host) {
	stored_host = host;
}

static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (stored_host == NULL || stored_host->call == NULL) {
		return 1;
	}
	return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static void free_host_buffer(void* ptr, size_t len) {
	if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
		stored_host->free_buffer(ptr, len);
	}
}
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"unsafe"

	"github.com/pragmaticgrowth/cpa-plugins/projects/opencode-go/internal/core"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	C.store_host_api(host)
	plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelopeBytes("invalid_method", "method is required"))
		return 1
	}
	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, err := core.Dispatch(C.GoString(method), requestBytes, hostHTTPDo)
	if err != nil {
		writeResponse(response, errorEnvelopeBytes("plugin_error", err.Error()))
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, length C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
	_ = length
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {}

// hostHTTPDo implements core.HTTPDoer over the host.http.do callback.
func hostHTTPDo(req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	result, err := callHost(pluginabi.MethodHostHTTPDo, req)
	if err != nil {
		return pluginapi.HTTPResponse{}, err
	}
	var resp pluginapi.HTTPResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		return pluginapi.HTTPResponse{}, fmt.Errorf("decode host.http.do result: %w", err)
	}
	return resp, nil
}

// callHost invokes a host callback and unwraps the {ok,result,error} envelope.
func callHost(method string, payload any) (json.RawMessage, error) {
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal host callback %s: %w", method, err)
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))

	var response C.cliproxy_buffer
	var requestPtr *C.uint8_t
	if len(rawPayload) > 0 {
		cPayload := C.CBytes(rawPayload)
		if cPayload == nil {
			return nil, fmt.Errorf("allocate host callback payload %s", method)
		}
		defer C.free(cPayload)
		requestPtr = (*C.uint8_t)(cPayload)
	}
	code := C.call_host_api(cMethod, requestPtr, C.size_t(len(rawPayload)), &response)
	var rawResponse []byte
	if response.ptr != nil && response.len > 0 {
		rawResponse = C.GoBytes(response.ptr, C.int(response.len))
	}
	if response.ptr != nil {
		C.free_host_buffer(response.ptr, response.len)
	}
	if len(rawResponse) == 0 {
		return nil, fmt.Errorf("host callback %s returned no response, code=%d", method, int(code))
	}
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result,omitempty"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(rawResponse, &env); err != nil {
		return nil, fmt.Errorf("decode host callback envelope %s: %w", method, err)
	}
	if !env.OK {
		if env.Error != nil {
			return nil, fmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)
		}
		return nil, fmt.Errorf("host callback %s failed", method)
	}
	if code != 0 {
		return nil, fmt.Errorf("host callback %s returned code=%d", method, int(code))
	}
	return append(json.RawMessage(nil), env.Result...), nil
}

func errorEnvelopeBytes(code, message string) []byte {
	raw, _ := json.Marshal(map[string]any{
		"ok":    false,
		"error": map[string]string{"code": code, "message": message},
	})
	return raw
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}
```

- [ ] **Step 2: Write `Makefile`**

`projects/opencode-go/Makefile`:
```make
PLUGIN_ID := opencode-go
GOOS := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)
EXT := dylib
ifeq ($(GOOS),linux)
	EXT := so
endif
ifeq ($(GOOS),windows)
	EXT := dll
endif

.PHONY: build test tidy clean
build:
	CGO_ENABLED=1 go build -buildmode=c-shared -o $(PLUGIN_ID).$(EXT) .

test:
	go test ./internal/core/...

tidy:
	go mod tidy

clean:
	rm -f $(PLUGIN_ID).dylib $(PLUGIN_ID).so $(PLUGIN_ID).dll $(PLUGIN_ID).h
```

- [ ] **Step 3: Build the c-shared library**

Run: `cd projects/opencode-go && go mod tidy && make build`
Expected: builds `opencode-go.dylib` (and `opencode-go.h`) with no errors.

- [ ] **Step 4: Verify the required export**

Run: `nm -gU projects/opencode-go/opencode-go.dylib | grep cliproxy_plugin_init`
Expected: a line ending `_cliproxy_plugin_init` (symbol present).

- [ ] **Step 5: Ignore build artifacts + commit source**

Create `projects/opencode-go/.gitignore`:
```
opencode-go.dylib
opencode-go.so
opencode-go.dll
opencode-go.h
```

```bash
git add projects/opencode-go/main.go projects/opencode-go/Makefile projects/opencode-go/.gitignore projects/opencode-go/go.mod projects/opencode-go/go.sum
git commit -m "feat(opencode-go): cgo adapter + c-shared build"
```

---

### Task 6: Packaging files + repo convention

**Files:**
- Create: `projects/opencode-go/README.md`
- Create: `projects/opencode-go/config.snippet.yaml`
- Create: `projects/opencode-go/opencode-go.json.example`
- Modify: `/Users/serkan/cpa-plugins/CLAUDE.md`

- [ ] **Step 1: Create `config.snippet.yaml`**

`projects/opencode-go/config.snippet.yaml`:
```yaml
# Add to your CLIProxyAPI config.yaml to load the opencode-go plugin.
# The host scans <dir>/<GOOS>/<GOARCH>/opencode-go.<ext> first, then flat <dir>/.
plugins:
  enabled: true            # GLOBAL master switch — must be true or nothing loads
  dir: "plugins"           # where the host looks for plugin shared libraries
  configs:
    opencode-go:
      enabled: true        # per-plugin switch
      priority: 1
```

- [ ] **Step 2: Create `opencode-go.json.example`**

`projects/opencode-go/opencode-go.json.example`:
```json
{
  "type": "opencode-go",
  "api_key": "sk-REPLACE_WITH_YOUR_OPENCODE_GO_API_KEY",
  "base_url": "https://opencode.ai/zen/go/v1"
}
```

- [ ] **Step 3: Create `README.md`**

`projects/opencode-go/README.md`:
```markdown
# opencode-go — CLIProxyAPI provider plugin

Adds **OpenCode Go** (`https://opencode.ai/zen/go/v1`) to CLIProxyAPI as a
provider. Its 22 low-cost coding models are served through CLIProxyAPI's
OpenAI-/Claude-/Gemini-compatible surfaces.

## How it works

- Declares two capabilities: `auth_provider` (turns an `opencode-go.json`
  credential into a routing record) and `model_provider` (lists the catalogue,
  discovered live from `GET /models` with a baked-in fallback).
- Declares **no** executor: CLIProxyAPI's built-in OpenAI-compatible executor
  binds to the `opencode-go` provider key and does the `Bearer POST
  /chat/completions`. All 22 models are reachable through that one endpoint.

## Build

    make build            # CGO_ENABLED=1 go build -buildmode=c-shared -o opencode-go.dylib .
    make test             # unit tests (internal/core)

## Install

Copy the artifact into the host's plugin dir under GOOS/GOARCH:

    mkdir -p <plugins.dir>/$(go env GOOS)/$(go env GOARCH)
    cp opencode-go.dylib <plugins.dir>/$(go env GOOS)/$(go env GOARCH)/

## Credential

Create `<AuthDir>/opencode-go.json` (default AuthDir: `~/.cli-proxy-api`). Do
**not** commit it. Fill the key from your environment:

    OPENCODE_API_KEY=sk-... \
      jq -n --arg k "$OPENCODE_API_KEY" \
      '{type:"opencode-go", api_key:$k, base_url:"https://opencode.ai/zen/go/v1"}' \
      > ~/.cli-proxy-api/opencode-go.json

## Wire

Merge `config.snippet.yaml` into your CLIProxyAPI `config.yaml`, then restart
the host. Verify:

    curl -s localhost:<port>/v0/management/plugins       # opencode-go registered
    curl -s localhost:<port>/v1/models | grep opencode   # 22 models
    curl -s localhost:<port>/v1/chat/completions -d '{"model":"deepseek-v4-flash","messages":[{"role":"user","content":"hi"}]}'
```

- [ ] **Step 4: Update the repo CLAUDE.md to document the `projects/` convention**

In `/Users/serkan/cpa-plugins/CLAUDE.md`, under "## What this repo is (and isn't)", replace the `- **Isn't (yet):**` bullet with:
```markdown
- **Also hosts real plugins:** `projects/<plugin-id>/` holds actual CLIProxyAPI
  provider/translator plugins built with this tooling (owner decision 2026-07-19 —
  all plugins live in-repo, not in split consuming projects). First one:
  `projects/opencode-go/` (OpenCode Go provider). `templates/go-plugin/` remains
  the scaffold.
```

Add to "## Structure":
```markdown
- `projects/<plugin-id>/` — real, buildable CLIProxyAPI plugins (own `go.mod`,
  c-shared build). `projects/opencode-go/` is the OpenCode Go provider.
```

- [ ] **Step 5: Commit**

```bash
git add projects/opencode-go/README.md projects/opencode-go/config.snippet.yaml projects/opencode-go/opencode-go.json.example CLAUDE.md
git commit -m "docs(opencode-go): packaging files + projects/ convention"
```

---

### Task 7: End-to-end host validation

**Files:** none (integration). Requires a running CLIProxyAPI host and `$OPENCODE_API_KEY`.

- [ ] **Step 1: Validate the artifact ABI** — run `/cpa:validate` against `projects/opencode-go/opencode-go.dylib`.
Expected: exports `cliproxy_plugin_init`; ABI version 1; metadata Name=`opencode-go`, Version, Author, GitHubRepository present.

- [ ] **Step 2: Install + wire** — copy the dylib to `<plugins.dir>/$(go env GOOS)/$(go env GOARCH)/opencode-go.dylib`; merge `config.snippet.yaml`; write the credential file from `$OPENCODE_API_KEY` (Task 6 README one-liner). Use `/cpa:wire` to print the exact paths.

- [ ] **Step 3: Registration check** — start the host; `GET /v0/management/plugins`.
Expected: `opencode-go` listed with `auth_provider` + `model_provider`.

- [ ] **Step 4: Model discovery check** — `GET /v1/models`.
Expected: the 22 `opencode-go` model ids present (e.g. `kimi-k3`, `deepseek-v4-flash`, `minimax-m3`).

- [ ] **Step 5: End-to-end OpenAI-family** — `POST /v1/chat/completions` `{"model":"deepseek-v4-flash","messages":[{"role":"user","content":"Reply with: pong"}],"max_tokens":16}`.
Expected: HTTP 200, `object:"chat.completion"`, content from the live gateway.

- [ ] **Step 6: End-to-end Anthropic-family via universal endpoint** — same call with `"model":"minimax-m3"`.
Expected: HTTP 200 `chat.completion` (confirms the built-in executor + universal `/chat/completions` cover the whole catalogue).

- [ ] **Step 7: Fallback check** — temporarily block egress to `opencode.ai` (or set an invalid `base_url` in the credential), restart, `GET /v1/models`.
Expected: the 22 static models still list (live-discovery fallback), even though completions would then fail.

- [ ] **Step 8: Commit any fixes** discovered during integration, then this task is done.

---

## Self-Review

**1. Spec coverage** (against `docs/superpowers/specs/2026-07-19-opencode-go-provider-design.md`):
- §3 two-capability decision (no executor) → Tasks 1–5; registration asserts only `auth_provider`+`model_provider` (Task 1 test). ✔
- §4 auth_provider (identifier/parse/refresh/login-stub, Attributes{base_url,api_key}) → Task 2. ✔
- §5 model_provider (for_auth live + static fallback) → Tasks 3–4. ✔
- §6 config → simplified per YAGNI: base_url comes from the credential file/default; `ConfigFields` empty in v1 (noted in Task 1). The spec's `discovery` toggle is deferred (live-with-fallback is the fixed v1 behavior). **Deviation from spec §6 — acceptable v1 simplification; the credential file already carries base_url.** ✔ (flagged)
- §7 raw model ids (`Prefix:""`) → `buildAuth` sets no prefix; `modelInfo` uses raw ids. ✔
- §8 request-time flow → validated by Task 7 steps 5–6. ✔
- §9 repo layout + CLAUDE.md convention → Task 6. ✔
- §11 testing plan → Task 7 maps 1:1. ✔

**2. Placeholder scan:** no TBD/TODO in code steps; the only literal placeholder is `sk-REPLACE_WITH_YOUR_OPENCODE_GO_API_KEY` inside the committed `.example` (intentional, never a real key). ✔

**3. Type consistency:** `Dispatch(method, request, do)` signature is identical in Task 1 (def), Task 5 (call), and all tests. `HTTPDoer`, `ProviderKey`, `DefaultBaseURL`, `catalog`, `modelInfo`, `staticModels`, `modelsForAuth`, `buildAuth` names match across tasks. `pluginapi.*` structs are used verbatim on both marshal and unmarshal sides. Method constants (`pluginabi.MethodAuthParse` etc.) verified present in `pluginabi-types.go`. ✔

**One spec deviation recorded** (§6 config simplification) — within the spec's YAGNI ethos and non-goals; if config-driven `base_url`/`discovery` is wanted, add a follow-up task that parses the `{"config_yaml": ...}` register payload.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-07-19-opencode-go-provider.md`. Two execution options:

1. **Subagent-Driven (recommended)** — a fresh subagent per task, two-stage review between tasks, fast iteration.
2. **Inline Execution** — execute tasks in this session via executing-plans, batch execution with checkpoints.
