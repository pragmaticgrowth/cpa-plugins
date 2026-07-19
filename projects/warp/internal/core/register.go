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
			ConfigFields: []pluginapi.ConfigField{
				{Name: "use_warp_credits", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Spend Warp subscription credits (sets allow_use_of_warp_credits)."},
				{Name: "model_prefix", Type: pluginapi.ConfigFieldTypeString, Description: "Prefix applied to registered model IDs (default warp/)."},
				{Name: "client_version", Type: pluginapi.ConfigFieldTypeString, Description: "x-warp-client-version header sent to app.warp.dev; override when Warp bumps it."},
				{Name: "os_category", Type: pluginapi.ConfigFieldTypeString, Description: "x-warp-os-category header value."},
				{Name: "os_name", Type: pluginapi.ConfigFieldTypeString, Description: "x-warp-os-name header value."},
				{Name: "os_version", Type: pluginapi.ConfigFieldTypeString, Description: "x-warp-os-version header value."},
			},
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
