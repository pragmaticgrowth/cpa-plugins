package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

type fakeHost struct {
	mu     sync.Mutex
	chunks []string
	closed chan string
}

func newFakeHost() *fakeHost { return &fakeHost{closed: make(chan string, 1)} }
func (f *fakeHost) StreamEmit(id string, p []byte) error {
	f.mu.Lock()
	f.chunks = append(f.chunks, string(p))
	f.mu.Unlock()
	return nil
}
func (f *fakeHost) StreamClose(id, e string) error { f.closed <- e; return nil }
func (f *fakeHost) Log(string, string)             {}

func (f *fakeHost) joined() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.chunks, "")
}

func TestExecuteStream_EmitsChunks(t *testing.T) {
	_ = applyConfigYAML(nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(sseEvent("Hel")))
		w.Write([]byte(sseEvent("lo")))
		w.Write([]byte(finishedEvent()))
		w.Write([]byte("data: [DONE]\n"))
	}))
	defer srv.Close()
	oldClient, oldEnd := warpHTTPClient, warpAIEndpoint
	warpHTTPClient = srv.Client()
	warpAIEndpoint = srv.URL
	defer func() { warpHTTPClient = oldClient; warpAIEndpoint = oldEnd }()

	host := newFakeHost()
	er := map[string]any{
		"Model":       "warp/claude-4-sonnet",
		"StorageJSON": []byte(`{"type":"warp","access_token":"tok"}`),
		"Payload":     []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
		"stream_id":   "s-1",
	}
	raw, _ := json.Marshal(er)
	if _, err := Dispatch(pluginabi.MethodExecutorExecuteStream, raw, host); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-host.closed:
		if e != "" {
			t.Fatalf("stream closed with error: %s", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not close")
	}
	joined := host.joined()
	if !strings.Contains(joined, "Hel") || !strings.Contains(joined, "lo") || !strings.Contains(joined, "[DONE]") {
		t.Fatalf("chunks missing content: %q", joined)
	}
}

func TestExecuteStream_QuotaClosesWithError(t *testing.T) {
	_ = applyConfigYAML(nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("No AI requests remaining"))
	}))
	defer srv.Close()
	oldClient, oldEnd := warpHTTPClient, warpAIEndpoint
	warpHTTPClient = srv.Client()
	warpAIEndpoint = srv.URL
	defer func() { warpHTTPClient = oldClient; warpAIEndpoint = oldEnd }()

	host := newFakeHost()
	er := map[string]any{
		"Model":       "warp/gpt-5",
		"StorageJSON": []byte(`{"type":"warp","access_token":"tok"}`),
		"Payload":     []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
		"stream_id":   "s-2",
	}
	raw, _ := json.Marshal(er)
	if _, err := Dispatch(pluginabi.MethodExecutorExecuteStream, raw, host); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-host.closed:
		if !strings.Contains(e, "quota") {
			t.Fatalf("expected quota close error, got %q", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not close")
	}
}

func TestExecuteStream_NoHost(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"stream_id": "x", "StorageJSON": []byte(`{"type":"warp"}`)})
	if _, err := Dispatch(pluginabi.MethodExecutorExecuteStream, raw, nil); err == nil {
		t.Fatal("expected error when host bridge is nil")
	}
}
