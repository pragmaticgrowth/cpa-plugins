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
