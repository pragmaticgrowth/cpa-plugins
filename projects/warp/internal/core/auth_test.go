package core

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestAuthIdentifier(t *testing.T) {
	out, err := Dispatch(pluginabi.MethodAuthIdentifier, []byte(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !containsRaw(out, `"identifier":"warp"`) {
		t.Fatalf("bad identifier: %s", out)
	}
}

func TestAuthParse_AcceptsWarpCredential(t *testing.T) {
	req := `{"Provider":"warp","FileName":"warp.json","RawJSON":` +
		jsonBytes(`{"type":"warp","refresh_token":"RT"}`) + `}`
	out, err := Dispatch(pluginabi.MethodAuthParse, []byte(req), nil)
	if err != nil {
		t.Fatal(err)
	}
	var resp pluginapi.AuthParseResponse
	unwrapResult(t, out, &resp)
	if !resp.Handled {
		t.Fatalf("expected Handled true: %s", out)
	}
	if resp.Auth.Provider != "warp" {
		t.Fatalf("bad provider: %+v", resp.Auth)
	}
	if authFromResult(t, resp.Auth).RefreshToken != "RT" {
		t.Fatal("refresh token not preserved")
	}
}

func TestAuthParse_RejectsForeign(t *testing.T) {
	req := `{"Provider":"warp","RawJSON":` + jsonBytes(`{"type":"other"}`) + `}`
	out, _ := Dispatch(pluginabi.MethodAuthParse, []byte(req), nil)
	var resp pluginapi.AuthParseResponse
	unwrapResult(t, out, &resp)
	if resp.Handled {
		t.Fatal("should not handle foreign credential")
	}
}

func TestAuthRefresh_UpdatesToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"aa.eyJleHAiOjQ4MDAwMDAwMDB9.bb"}`))
	}))
	defer srv.Close()
	old := refreshEndpointVar
	refreshEndpointVar = srv.URL
	oldClient := warpHTTPClient
	warpHTTPClient = srv.Client()
	defer func() { refreshEndpointVar = old; warpHTTPClient = oldClient }()

	req := `{"AuthID":"warp","StorageJSON":` + jsonBytes(`{"type":"warp","refresh_token":"RT"}`) + `}`
	out, err := Dispatch(pluginabi.MethodAuthRefresh, []byte(req), nil)
	if err != nil {
		t.Fatal(err)
	}
	var resp pluginapi.AuthRefreshResponse
	unwrapResult(t, out, &resp)
	cred := authFromResult(t, resp.Auth)
	if !strings.HasPrefix(cred.AccessToken, "aa.") {
		t.Fatalf("token not refreshed: %q", cred.AccessToken)
	}
	if resp.Auth.ID != "warp" {
		t.Fatalf("auth id = %q", resp.Auth.ID)
	}
	if resp.NextRefreshAfter.IsZero() {
		t.Fatal("next refresh not set")
	}
}

func TestAuthLoginPoll_ReturnsError(t *testing.T) {
	out, err := Dispatch(pluginabi.MethodAuthLoginPoll, []byte(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var resp pluginapi.AuthLoginPollResponse
	unwrapResult(t, out, &resp)
	if resp.Status != pluginapi.AuthLoginStatusError {
		t.Fatalf("expected error status, got %q", resp.Status)
	}
}
