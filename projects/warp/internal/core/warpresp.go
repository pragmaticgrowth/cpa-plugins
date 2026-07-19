package core

import (
	"bufio"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	warppb "github.com/warpdotdev/warp-proto-apis/apis/multi_agent/v1/gen/go"
	"google.golang.org/protobuf/proto"
)

// newSSEScanner returns a line scanner sized for large protobuf-over-SSE lines.
func newSSEScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	return sc
}

// decodeSSELine parses one `data:` line. Returns done=true for the [DONE] sentinel,
// ok=true when a ResponseEvent was decoded.
func decodeSSELine(line string) (*warppb.ResponseEvent, bool, bool, error) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "data:") {
		return nil, false, false, nil
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" {
		return nil, false, false, nil
	}
	if payload == "[DONE]" {
		return nil, true, false, nil
	}
	// The wire encoding varies (base64url / hex / std base64) and the alphabets
	// overlap, so decode defensively: accept whichever candidate decoding yields
	// a valid ResponseEvent proto.
	cands := candidateDecodes(payload)
	if len(cands) == 0 {
		return nil, false, false, fmt.Errorf("undecodable SSE payload")
	}
	var lastErr error
	for _, raw := range cands {
		var ev warppb.ResponseEvent
		if err := proto.Unmarshal(raw, &ev); err == nil {
			return &ev, false, true, nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no candidate decoding produced a valid ResponseEvent")
	}
	return nil, false, false, lastErr
}

// candidateDecodes returns the possible byte decodings of an SSE payload, in
// priority order (base64url, hex, std base64), skipping those that error.
func candidateDecodes(s string) [][]byte {
	s = strings.Trim(s, `"`)
	s = strings.Join(strings.Fields(s), "")
	pad := (4 - len(s)%4) % 4
	var out [][]byte
	if b, err := base64.URLEncoding.DecodeString(s + strings.Repeat("=", pad)); err == nil {
		out = append(out, b)
	}
	if b, err := hex.DecodeString(s); err == nil {
		out = append(out, b)
	}
	if b, err := base64.StdEncoding.DecodeString(s + strings.Repeat("=", pad)); err == nil {
		out = append(out, b)
	}
	return out
}

// eventText extracts assistant text deltas from a ResponseEvent (empty if none).
func eventText(ev *warppb.ResponseEvent) string {
	ca := ev.GetClientActions()
	if ca == nil {
		return ""
	}
	var sb strings.Builder
	for _, a := range ca.GetActions() {
		if app := a.GetAppendToMessageContent(); app != nil {
			sb.WriteString(app.GetMessage().GetAgentOutput().GetText())
		}
		if add := a.GetAddMessagesToTask(); add != nil {
			for _, m := range add.GetMessages() {
				sb.WriteString(m.GetAgentOutput().GetText())
			}
		}
	}
	return sb.String()
}

// finishReason returns an OpenAI finish reason when the event is terminal.
func finishReason(ev *warppb.ResponseEvent) (string, bool) {
	fin := ev.GetFinished()
	if fin == nil {
		return "", false
	}
	switch {
	case fin.GetMaxTokenLimit() != nil:
		return "length", true
	default:
		return "stop", true
	}
}

// streamError returns a non-empty error message when a StreamFinished event
// carries a failure reason (quota / internal / llm-unavailable / invalid key).
func streamError(ev *warppb.ResponseEvent) string {
	fin := ev.GetFinished()
	if fin == nil {
		return ""
	}
	switch {
	case fin.GetQuotaLimit() != nil:
		return "warp_quota_exhausted: no remaining Warp AI quota"
	case fin.GetInvalidApiKey() != nil:
		return "warp_invalid_api_key"
	case fin.GetLlmUnavailable() != nil:
		return "warp_llm_unavailable"
	case fin.GetInternalError() != nil:
		msg := fin.GetInternalError().GetMessage()
		if msg == "" {
			msg = "internal error"
		}
		return "warp_internal_error: " + msg
	}
	return ""
}

// chatChunkJSON builds one chat-completions SSE data payload (without the "data: " prefix).
func chatChunkJSON(model, delta, finish string) []byte {
	choice := map[string]any{"index": 0, "delta": map[string]any{}}
	if delta != "" {
		choice["delta"] = map[string]any{"content": delta}
	}
	if finish != "" {
		choice["finish_reason"] = finish
	}
	obj := map[string]any{
		"object":  "chat.completion.chunk",
		"model":   model,
		"choices": []any{choice},
	}
	b, _ := json.Marshal(obj)
	return b
}

func chatFullJSON(model, content, finish string) []byte {
	if finish == "" {
		finish = "stop"
	}
	obj := map[string]any{
		"object": "chat.completion",
		"model":  model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": content},
			"finish_reason": finish,
		}},
	}
	b, _ := json.Marshal(obj)
	return b
}
