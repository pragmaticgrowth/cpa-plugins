package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// warpAIEndpoint is the multi-agent endpoint. Overridable in tests.
var warpAIEndpoint = "https://app.warp.dev/ai/multi-agent"

func executorIdentifier() (json.RawMessage, error) {
	return okEnvelope(map[string]string{"identifier": "warp"})
}

// postWarp sends the serialized protobuf request and returns the raw SSE response.
func postWarp(ctx context.Context, cfg Config, cred Credential, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, warpAIEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/x-protobuf")
	req.Header.Set("accept", "text/event-stream")
	req.Header.Set("authorization", "Bearer "+cred.AccessToken)
	req.Header.Set("x-warp-client-version", cfg.ClientVersion)
	req.Header.Set("x-warp-os-category", cfg.OSCategory)
	req.Header.Set("x-warp-os-name", cfg.OSName)
	req.Header.Set("x-warp-os-version", cfg.OSVersion)
	return warpHTTPClient.Do(req)
}

// quotaError maps upstream 429 / quota to a stable error.
func quotaError(status int, body string) error {
	if status == http.StatusTooManyRequests ||
		strings.Contains(body, "No remaining quota") ||
		strings.Contains(body, "No AI requests remaining") {
		return fmt.Errorf("warp_quota_exhausted: %s", strings.TrimSpace(body))
	}
	return fmt.Errorf("warp upstream status %d: %s", status, strings.TrimSpace(body))
}

// decodeExecCredential unmarshals the ExecutorRequest StorageJSON into a Credential.
func decodeExecCredential(storage []byte) (Credential, error) {
	var cred Credential
	if len(storage) == 0 {
		return cred, fmt.Errorf("missing warp credential")
	}
	if err := json.Unmarshal(storage, &cred); err != nil {
		return cred, fmt.Errorf("decode warp credential: %w", err)
	}
	return cred, nil
}

func handleExecute(request []byte) (json.RawMessage, error) {
	var er pluginapi.ExecutorRequest
	if err := json.Unmarshal(request, &er); err != nil {
		return nil, err
	}
	cfg := CurrentConfig()
	cred, err := decodeExecCredential(er.StorageJSON)
	if err != nil {
		return nil, err
	}
	cr, err := parseChatRequest(er.Payload)
	if err != nil {
		return nil, err
	}
	if cr.Model == "" {
		cr.Model = er.Model
	}
	body, err := BuildWarpRequest(cfg, cr)
	if err != nil {
		return nil, err
	}
	resp, err := postWarp(context.Background(), cfg, cred, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, quotaError(resp.StatusCode, string(b))
	}

	var content strings.Builder
	finish := "stop"
	sc := newSSEScanner(resp.Body)
	for sc.Scan() {
		ev, done, ok, derr := decodeSSELine(sc.Text())
		if derr != nil {
			continue // skip undecodable lines defensively
		}
		if done {
			break
		}
		if !ok {
			continue
		}
		if se := streamError(ev); se != "" {
			return nil, fmt.Errorf("%s", se)
		}
		content.WriteString(eventText(ev))
		if r, term := finishReason(ev); term {
			finish = r
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	full := chatFullJSON(er.Model, content.String(), finish)
	return okEnvelope(pluginapi.ExecutorResponse{
		Payload: full,
		Headers: http.Header{"Content-Type": []string{"application/json"}},
	})
}
