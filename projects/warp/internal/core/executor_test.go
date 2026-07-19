package core

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	warppb "github.com/warpdotdev/warp-proto-apis/apis/multi_agent/v1/gen/go"
	"google.golang.org/protobuf/proto"
)

// sseEvent returns one Warp SSE data line carrying an assistant text delta.
func sseEvent(text string) string {
	raw, _ := proto.Marshal(appendTextEvent(text))
	return "data: " + base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(raw) + "\n"
}

// finishedEvent returns one SSE data line for a graceful StreamFinished.
func finishedEvent() string {
	ev := warppb.ResponseEvent_builder{
		Finished: warppb.ResponseEvent_StreamFinished_builder{
			Done: warppb.ResponseEvent_StreamFinished_Done_builder{}.Build(),
		}.Build(),
	}.Build()
	raw, _ := proto.Marshal(ev)
	return "data: " + base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(raw) + "\n"
}

func TestExecute_NonStreaming(t *testing.T) {
	_ = applyConfigYAML(nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("authorization") != "Bearer tok" {
			t.Errorf("missing bearer auth: %q", r.Header.Get("authorization"))
		}
		if r.Header.Get("x-warp-client-version") == "" {
			t.Error("missing client version header")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(sseEvent("2 + 2 = 4")))
		w.Write([]byte(finishedEvent()))
		w.Write([]byte("data: [DONE]\n"))
	}))
	defer srv.Close()
	oldClient, oldEnd := warpHTTPClient, warpAIEndpoint
	warpHTTPClient = srv.Client()
	warpAIEndpoint = srv.URL
	defer func() { warpHTTPClient = oldClient; warpAIEndpoint = oldEnd }()

	er := map[string]any{
		"Model":       "warp/claude-4-sonnet",
		"StorageJSON": []byte(`{"type":"warp","refresh_token":"RT","access_token":"tok"}`),
		"Payload":     []byte(`{"model":"warp/claude-4-sonnet","messages":[{"role":"user","content":"2+2?"}]}`),
	}
	raw, _ := json.Marshal(er)
	out, err := Dispatch(pluginabi.MethodExecutorExecute, raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	var resp pluginapi.ExecutorResponse
	unwrapResult(t, out, &resp)

	var chat struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(resp.Payload, &chat); err != nil {
		t.Fatalf("bad chat payload: %v (%s)", err, resp.Payload)
	}
	if chat.Choices[0].Message.Content != "2 + 2 = 4" {
		t.Fatalf("content = %q", chat.Choices[0].Message.Content)
	}
	if chat.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish = %q", chat.Choices[0].FinishReason)
	}
}

func TestExecute_QuotaError(t *testing.T) {
	_ = applyConfigYAML(nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("No remaining quota"))
	}))
	defer srv.Close()
	oldClient, oldEnd := warpHTTPClient, warpAIEndpoint
	warpHTTPClient = srv.Client()
	warpAIEndpoint = srv.URL
	defer func() { warpHTTPClient = oldClient; warpAIEndpoint = oldEnd }()

	er := map[string]any{
		"Model":       "warp/gpt-5",
		"StorageJSON": []byte(`{"type":"warp","access_token":"tok"}`),
		"Payload":     []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
	}
	raw, _ := json.Marshal(er)
	_, err := Dispatch(pluginabi.MethodExecutorExecute, raw, nil)
	if err == nil {
		t.Fatal("expected quota error")
	}
}

func TestCountTokens_Estimates(t *testing.T) {
	er := map[string]any{"Payload": []byte(`{"messages":[{"role":"user","content":"abcdefgh"}]}`)}
	raw, _ := json.Marshal(er)
	out, err := Dispatch(pluginabi.MethodExecutorCountTokens, raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	var resp pluginapi.ExecutorResponse
	unwrapResult(t, out, &resp)
	var counts struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.Unmarshal(resp.Payload, &counts); err != nil {
		t.Fatalf("bad count payload: %v (%s)", err, resp.Payload)
	}
	if counts.InputTokens <= 0 {
		t.Fatalf("expected positive estimate, got %d", counts.InputTokens)
	}
}

func TestHTTPRequest_NoOp(t *testing.T) {
	out, err := Dispatch(pluginabi.MethodExecutorHTTPRequest, []byte(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var resp pluginapi.ExecutorResponse
	unwrapResult(t, out, &resp)
}
