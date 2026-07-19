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
