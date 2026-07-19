package core

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// keychainReader returns the raw JSON stored under dev.warp.Warp-Stable (macOS).
// Overridable in tests.
var keychainReader = func() (string, error) {
	out, err := exec.Command("security", "find-generic-password",
		"-s", "dev.warp.Warp-Stable", "-w").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func handleCLIRegister(raw []byte) (json.RawMessage, error) {
	return okEnvelope(pluginapi.CommandLineRegistrationResponse{
		Flags: []pluginapi.CommandLineFlag{
			{Name: "warp-login", Usage: "Import Warp credentials from the local Keychain and save them.", Type: "bool", DefaultValue: "false"},
			{Name: "warp-refresh-token", Usage: "Provide a Warp Firebase refresh token directly instead of reading the Keychain.", Type: "string", DefaultValue: ""},
		},
	})
}

func handleCLIExecute(raw []byte) (json.RawMessage, error) {
	var req pluginapi.CommandLineExecutionRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	// Only act when one of our flags triggered.
	if _, ok := req.TriggeredFlags["warp-login"]; !ok {
		if _, ok2 := req.TriggeredFlags["warp-refresh-token"]; !ok2 {
			return okEnvelope(pluginapi.CommandLineExecutionResponse{})
		}
	}

	refresh := ""
	if v, ok := req.Flags["warp-refresh-token"]; ok && v.Value != "" {
		refresh = strings.TrimSpace(v.Value)
	}
	if refresh == "" {
		if v, ok := req.TriggeredFlags["warp-refresh-token"]; ok && v.Value != "" {
			refresh = strings.TrimSpace(v.Value)
		}
	}
	if refresh == "" {
		blob, err := keychainReader()
		if err != nil {
			return okEnvelope(pluginapi.CommandLineExecutionResponse{
				Stderr:   []byte("could not read Warp Keychain entry; pass --warp-refresh-token: " + err.Error() + "\n"),
				ExitCode: 1,
			})
		}
		refresh, err = extractRefreshToken(blob)
		if err != nil {
			return okEnvelope(pluginapi.CommandLineExecutionResponse{
				Stderr: []byte(err.Error() + "\n"), ExitCode: 1})
		}
	}

	access, exp, err := RefreshAccessToken(warpHTTPClient, refreshEndpointVar, FirebaseKey, refresh)
	if err != nil {
		return okEnvelope(pluginapi.CommandLineExecutionResponse{
			Stderr: []byte("initial token refresh failed: " + err.Error() + "\n"), ExitCode: 1})
	}
	cred := Credential{Type: "warp", RefreshToken: refresh, AccessToken: access, ExpiresAt: exp}
	auth := credentialToAuthData("warp.json", cred)
	return okEnvelope(pluginapi.CommandLineExecutionResponse{
		Stdout: []byte("Warp credential imported and verified. Saved as warp.json.\n"),
		Auths:  []pluginapi.AuthData{auth},
	})
}

// extractRefreshToken pulls refresh_token from the Warp Keychain JSON blob,
// tolerating both flat and nested {"id_token":{"refresh_token":...}} shapes.
func extractRefreshToken(blob string) (string, error) {
	var flat struct {
		RefreshToken string `json:"refresh_token"`
	}
	if json.Unmarshal([]byte(blob), &flat) == nil && flat.RefreshToken != "" {
		return flat.RefreshToken, nil
	}
	var nested struct {
		IDToken struct {
			RefreshToken string `json:"refresh_token"`
		} `json:"id_token"`
	}
	if json.Unmarshal([]byte(blob), &nested) == nil && nested.IDToken.RefreshToken != "" {
		return nested.IDToken.RefreshToken, nil
	}
	return "", fmt.Errorf("no refresh_token found in Warp Keychain payload")
}
