package core

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// FirebaseKey is Warp's public Firebase web API key (ships in every client).
const FirebaseKey = "AIzaSyBdy3O3S9hrdayLJxJ7mriBR4qgUaUygAs"

// Credential is the plugin-owned auth blob persisted as the auth file's JSON.
type Credential struct {
	Type         string    `json:"type"`
	RefreshToken string    `json:"refresh_token"`
	AccessToken  string    `json:"access_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	Email        string    `json:"email,omitempty"`
}

// NextRefresh returns 5 minutes before expiry (or now-ish if unknown/expired).
func (c Credential) NextRefresh() time.Time {
	if c.ExpiresAt.IsZero() {
		return time.Now()
	}
	return c.ExpiresAt.Add(-5 * time.Minute)
}

// jwtExpiry decodes a JWT's "exp" claim without verifying the signature.
func jwtExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0).UTC(), true
}
