package core

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRefreshAccessToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "RT" {
			w.WriteHeader(400)
			return
		}
		// access_token with an exp claim ~far future
		w.Write([]byte(`{"access_token":"aa.eyJleHAiOjQ4MDAwMDAwMDB9.bb","expires_in":"3600"}`))
	}))
	defer srv.Close()

	access, exp, err := RefreshAccessToken(srv.Client(), srv.URL, "KEY", "RT")
	if err != nil || access == "" {
		t.Fatalf("refresh err=%v access=%q", err, access)
	}
	if exp.IsZero() {
		t.Fatal("expiry not set")
	}
}

func TestRefreshAccessToken_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":"INVALID_REFRESH_TOKEN"}`))
	}))
	defer srv.Close()

	if _, _, err := RefreshAccessToken(srv.Client(), srv.URL, "KEY", "RT"); err == nil {
		t.Fatal("expected error on non-200")
	}
}
