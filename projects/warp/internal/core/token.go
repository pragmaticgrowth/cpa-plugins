package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const refreshEndpoint = "https://app.warp.dev/proxy/token"

// refreshEndpointVar is the effective refresh endpoint. It defaults to the real
// Warp endpoint but is overridable in tests.
var refreshEndpointVar = refreshEndpoint

// RefreshAccessToken exchanges a Firebase refresh token for a fresh access JWT.
func RefreshAccessToken(client *http.Client, endpoint, firebaseKey, refreshToken string) (string, time.Time, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)

	u := endpoint + "?key=" + url.QueryEscape(firebaseKey)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("refresh status %d: %s", resp.StatusCode, string(body))
	}

	// Decode permissively — only access_token is guaranteed.
	var out struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
		ExpiresIn   string `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", time.Time{}, fmt.Errorf("decode refresh: %w", err)
	}
	access := out.AccessToken
	if access == "" {
		access = out.IDToken
	}
	if access == "" {
		return "", time.Time{}, fmt.Errorf("refresh response missing access_token")
	}
	// Prefer the JWT's own exp; fall back to expires_in; else +55m.
	exp, ok := jwtExpiry(access)
	if !ok {
		if secs, e := strconv.Atoi(out.ExpiresIn); e == nil && secs > 0 {
			exp = time.Now().Add(time.Duration(secs) * time.Second)
		} else {
			exp = time.Now().Add(55 * time.Minute)
		}
	}
	return access, exp, nil
}
