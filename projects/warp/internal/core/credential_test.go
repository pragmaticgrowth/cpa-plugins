package core

import (
	"encoding/base64"
	"strconv"
	"testing"
	"time"
)

func TestJWTExpiry(t *testing.T) {
	exp := time.Now().Add(time.Hour).Unix()
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":` + strconv.FormatInt(exp, 10) + `}`))
	tok := "aaa." + payload + ".bbb"
	got, ok := jwtExpiry(tok)
	if !ok || got.Unix() != exp {
		t.Fatalf("exp mismatch ok=%v got=%v want=%v", ok, got.Unix(), exp)
	}
}

func TestJWTExpiry_Malformed(t *testing.T) {
	if _, ok := jwtExpiry("not-a-jwt"); ok {
		t.Fatal("expected malformed token to fail")
	}
}

func TestCredential_NextRefresh(t *testing.T) {
	exp := time.Now().Add(time.Hour).UTC()
	c := Credential{ExpiresAt: exp}
	if got := c.NextRefresh(); !got.Equal(exp.Add(-5 * time.Minute)) {
		t.Fatalf("next refresh = %v", got)
	}
	// Zero expiry => refresh ~now.
	zero := Credential{}
	if zero.NextRefresh().After(time.Now().Add(time.Second)) {
		t.Fatal("zero-expiry next refresh should be ~now")
	}
}
