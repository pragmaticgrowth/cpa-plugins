package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// execStreamRequest is an ExecutorRequest plus the wire-only stream_id the host
// assigns for host.stream.emit / host.stream.close.
type execStreamRequest struct {
	pluginapi.ExecutorRequest
	StreamID string `json:"stream_id"`
}

func handleExecuteStream(request []byte, host HostBridge) (json.RawMessage, error) {
	var er execStreamRequest
	if err := json.Unmarshal(request, &er); err != nil {
		return nil, err
	}
	if host == nil {
		return nil, fmt.Errorf("streaming requires host bridge")
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
	streamID := er.StreamID
	model := er.Model

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				_ = host.StreamClose(streamID, fmt.Sprintf("panic: %v", rec))
			}
		}()
		if runErr := runWarpStream(context.Background(), cfg, cred, cr, model, streamID, host); runErr != nil {
			_ = host.StreamClose(streamID, runErr.Error())
			return
		}
		_ = host.StreamClose(streamID, "")
	}()

	// Return headers immediately; chunks flow via host.stream.emit.
	return okEnvelope(map[string]any{
		"headers": http.Header{"Content-Type": []string{"text/event-stream"}},
	})
}

func runWarpStream(ctx context.Context, cfg Config, cred Credential, cr ChatRequest, model, streamID string, host HostBridge) error {
	body, err := BuildWarpRequest(cfg, cr)
	if err != nil {
		return err
	}
	resp, err := postWarp(ctx, cfg, cred, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return quotaError(resp.StatusCode, string(b))
	}

	emit := func(payload []byte) error {
		out := append([]byte("data: "), payload...)
		out = append(out, '\n', '\n')
		return host.StreamEmit(streamID, out)
	}

	sc := newSSEScanner(resp.Body)
	finish := "stop"
	for sc.Scan() {
		ev, done, ok, derr := decodeSSELine(sc.Text())
		if derr != nil {
			continue
		}
		if done {
			break
		}
		if !ok {
			continue
		}
		if se := streamError(ev); se != "" {
			return fmt.Errorf("%s", se)
		}
		if txt := eventText(ev); txt != "" {
			if e := emit(chatChunkJSON(model, txt, "")); e != nil {
				return e
			}
		}
		if r, term := finishReason(ev); term {
			finish = r
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if e := emit(chatChunkJSON(model, "", finish)); e != nil {
		return e
	}
	return host.StreamEmit(streamID, []byte("data: [DONE]\n\n"))
}
