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
