package core

import (
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestModelRegister_PrefixedAndOwned(t *testing.T) {
	_ = applyConfigYAML(nil) // model_prefix "warp/"
	out, err := Dispatch(pluginabi.MethodModelRegister, []byte(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var resp pluginapi.ModelRegistrationResponse
	unwrapResult(t, out, &resp)
	if resp.Provider != "warp" {
		t.Fatalf("provider = %q", resp.Provider)
	}
	found := false
	for _, m := range resp.Models {
		if m.ID == "warp/claude-4-sonnet" {
			found = true
			if m.OwnedBy != "warp" {
				t.Fatalf("owned by = %q", m.OwnedBy)
			}
		}
		if !strings.HasPrefix(m.ID, "warp/") {
			t.Fatalf("model missing prefix: %q", m.ID)
		}
	}
	if !found {
		t.Fatalf("warp/claude-4-sonnet not in catalogue: %+v", resp.Models)
	}
}

func TestStripModelPrefix(t *testing.T) {
	_ = applyConfigYAML(nil)
	if stripModelPrefix("warp/gpt-5") != "gpt-5" {
		t.Fatal("strip failed")
	}
	if prefixedModelID("gpt-5") != "warp/gpt-5" {
		t.Fatal("prefix failed")
	}
}
