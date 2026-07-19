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
