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
