package core

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// containsRaw reports whether the marshaled envelope contains needle.
func containsRaw(env json.RawMessage, needle string) bool {
	return len(env) > 0 && strings.Contains(string(env), needle)
}

// jsonString returns s as a JSON-quoted string literal.
func jsonString(s string) string { b, _ := json.Marshal(s); return string(b) }

// jsonBytes returns s encoded as a JSON []byte value (base64-quoted), matching
// how the host serializes []byte fields such as RawJSON / StorageJSON / Payload.
func jsonBytes(s string) string { b, _ := json.Marshal([]byte(s)); return string(b) }

// unwrapResult unmarshals an {ok,result} envelope's result into v.
func unwrapResult(t *testing.T, env json.RawMessage, v any) {
	t.Helper()
	var e struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(env, &e); err != nil {
		t.Fatalf("bad envelope: %v (%s)", err, env)
	}
	if !e.OK {
		t.Fatalf("envelope not ok: %s", env)
	}
	if err := json.Unmarshal(e.Result, v); err != nil {
		t.Fatalf("bad result: %v (%s)", err, e.Result)
	}
}

// authFromResult decodes the AuthData in an auth-parse/refresh result and
// returns the embedded Warp Credential.
func authFromResult(t *testing.T, auth pluginapi.AuthData) Credential {
	t.Helper()
	var cred Credential
	if err := json.Unmarshal(auth.StorageJSON, &cred); err != nil {
		t.Fatalf("decode storage json: %v", err)
	}
	return cred
}
