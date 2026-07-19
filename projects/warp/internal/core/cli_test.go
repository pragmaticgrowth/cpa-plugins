package core

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestCLIRegister_DeclaresFlags(t *testing.T) {
	out, err := Dispatch(pluginabi.MethodCommandLineRegister, []byte(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var resp pluginapi.CommandLineRegistrationResponse
	unwrapResult(t, out, &resp)
	names := map[string]bool{}
	for _, f := range resp.Flags {
		names[f.Name] = true
	}
	if !names["warp-login"] || !names["warp-refresh-token"] {
		t.Fatalf("missing flags: %+v", resp.Flags)
	}
}

func TestCLIExecute_ImportsFromKeychain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"aa.eyJleHAiOjQ4MDAwMDAwMDB9.bb"}`))
	}))
	defer srv.Close()
	oldClient, oldEnd := warpHTTPClient, refreshEndpointVar
	warpHTTPClient = srv.Client()
	refreshEndpointVar = srv.URL
	oldReader := keychainReader
	keychainReader = func() (string, error) { return `{"id_token":{"refresh_token":"RT-123"}}`, nil }
	defer func() {
		warpHTTPClient = oldClient
		refreshEndpointVar = oldEnd
		keychainReader = oldReader
	}()

	req := `{"TriggeredFlags":{"warp-login":{"Name":"warp-login","Type":"bool","Value":"true","Set":true}},"Flags":{}}`
	out, err := Dispatch(pluginabi.MethodCommandLineExecute, []byte(req), nil)
	if err != nil {
		t.Fatal(err)
	}
	var resp pluginapi.CommandLineExecutionResponse
	unwrapResult(t, out, &resp)
	if len(resp.Auths) != 1 {
		t.Fatalf("expected one auth, got %d (stderr=%s)", len(resp.Auths), resp.Stderr)
	}
	cred := authFromResult(t, resp.Auths[0])
	if cred.RefreshToken != "RT-123" || cred.AccessToken == "" {
		t.Fatalf("bad imported credential: %+v", cred)
	}
	if !strings.Contains(string(resp.Stdout), "imported") {
		t.Fatalf("expected confirmation in stdout: %q", resp.Stdout)
	}
}

func TestCLIExecute_NotTriggered(t *testing.T) {
	out, err := Dispatch(pluginabi.MethodCommandLineExecute, []byte(`{"TriggeredFlags":{},"Flags":{}}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var resp pluginapi.CommandLineExecutionResponse
	unwrapResult(t, out, &resp)
	if len(resp.Auths) != 0 {
		t.Fatal("should not import when not triggered")
	}
}

func TestExtractRefreshToken_NestedAndFlat(t *testing.T) {
	if v, _ := extractRefreshToken(`{"refresh_token":"A"}`); v != "A" {
		t.Fatal("flat failed")
	}
	if v, _ := extractRefreshToken(`{"id_token":{"refresh_token":"B"}}`); v != "B" {
		t.Fatal("nested failed")
	}
	if _, err := extractRefreshToken(`{}`); err == nil {
		t.Fatal("expected error for empty payload")
	}
}
