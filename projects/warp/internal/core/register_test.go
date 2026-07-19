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
	if reg.Metadata.Name != "warp" {
		t.Fatalf("bad metadata name: %q", reg.Metadata.Name)
	}
}
