package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// warpHTTPClient is used for all Warp network I/O (auto HTTP/2 over TLS). No
// global timeout because execute_stream is long-lived; per-call contexts bound
// the refresh exchange.
var warpHTTPClient = &http.Client{Timeout: 0}

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
		return okEnvelope(pluginapi.AuthParseResponse{Handled: false})
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
		return nil, fmt.Errorf("decode warp credential: %w", err)
	}
	if cred.RefreshToken == "" {
		return nil, fmt.Errorf("warp refresh: no refresh_token in stored credential")
	}
	access, exp, err := RefreshAccessToken(warpHTTPClient, refreshEndpointVar, FirebaseKey, cred.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("warp refresh: %w", err)
	}
	cred.AccessToken = access
	cred.ExpiresAt = exp
	fileName := req.AuthID
	if fileName == "" {
		fileName = "warp"
	}
	auth := credentialToAuthData(fileName+".json", cred)
	if req.AuthID != "" {
		auth.ID = req.AuthID
	}
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
