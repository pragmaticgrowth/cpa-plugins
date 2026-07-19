package core

import (
	"encoding/base64"
	"testing"

	warppb "github.com/warpdotdev/warp-proto-apis/apis/multi_agent/v1/gen/go"
	"google.golang.org/protobuf/proto"
)

func encodeEvent(ev *warppb.ResponseEvent) string {
	raw, _ := proto.Marshal(ev)
	return "data: " + base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(raw)
}

func appendTextEvent(text string) *warppb.ResponseEvent {
	return warppb.ResponseEvent_builder{
		ClientActions: warppb.ResponseEvent_ClientActions_builder{
			Actions: []*warppb.ClientAction{
				warppb.ClientAction_builder{
					AppendToMessageContent: warppb.ClientAction_AppendToMessageContent_builder{
						Message: warppb.Message_builder{
							AgentOutput: warppb.Message_AgentOutput_builder{Text: proto.String(text)}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			},
		}.Build(),
	}.Build()
}

func TestDecodeSSE_TextDelta(t *testing.T) {
	got, done, ok, err := decodeSSELine(encodeEvent(appendTextEvent("Hello")))
	if err != nil || !ok || done {
		t.Fatalf("decode: err=%v ok=%v done=%v", err, ok, done)
	}
	if eventText(got) != "Hello" {
		t.Fatalf("text = %q", eventText(got))
	}
}

func TestDecodeSSE_AddMessagesToTask(t *testing.T) {
	ev := warppb.ResponseEvent_builder{
		ClientActions: warppb.ResponseEvent_ClientActions_builder{
			Actions: []*warppb.ClientAction{
				warppb.ClientAction_builder{
					AddMessagesToTask: warppb.ClientAction_AddMessagesToTask_builder{
						Messages: []*warppb.Message{
							warppb.Message_builder{
								AgentOutput: warppb.Message_AgentOutput_builder{Text: proto.String("world")}.Build(),
							}.Build(),
						},
					}.Build(),
				}.Build(),
			},
		}.Build(),
	}.Build()
	got, _, ok, err := decodeSSELine(encodeEvent(ev))
	if err != nil || !ok {
		t.Fatalf("decode err=%v ok=%v", err, ok)
	}
	if eventText(got) != "world" {
		t.Fatalf("text = %q", eventText(got))
	}
}

func TestDecodeSSE_Done(t *testing.T) {
	_, done, _, _ := decodeSSELine("data: [DONE]")
	if !done {
		t.Fatal("expected done")
	}
}

func TestDecodeSSE_NonData(t *testing.T) {
	_, done, ok, err := decodeSSELine(": keep-alive")
	if err != nil || ok || done {
		t.Fatalf("comment line should be ignored: ok=%v done=%v err=%v", ok, done, err)
	}
}

func TestFinishReason(t *testing.T) {
	ev := warppb.ResponseEvent_builder{
		Finished: warppb.ResponseEvent_StreamFinished_builder{}.Build(),
	}.Build()
	if r, ok := finishReason(ev); !ok || r != "stop" {
		t.Fatalf("finish = %q ok=%v", r, ok)
	}
	evMax := warppb.ResponseEvent_builder{
		Finished: warppb.ResponseEvent_StreamFinished_builder{
			MaxTokenLimit: warppb.ResponseEvent_StreamFinished_ReachedMaxTokenLimit_builder{}.Build(),
		}.Build(),
	}.Build()
	if r, ok := finishReason(evMax); !ok || r != "length" {
		t.Fatalf("max-token finish = %q ok=%v", r, ok)
	}
}

func TestStreamError_Quota(t *testing.T) {
	ev := warppb.ResponseEvent_builder{
		Finished: warppb.ResponseEvent_StreamFinished_builder{
			QuotaLimit: warppb.ResponseEvent_StreamFinished_QuotaLimit_builder{}.Build(),
		}.Build(),
	}.Build()
	if streamError(ev) == "" {
		t.Fatal("expected quota stream error")
	}
	// A plain finished event carries no error.
	ok := warppb.ResponseEvent_builder{Finished: warppb.ResponseEvent_StreamFinished_builder{}.Build()}.Build()
	if streamError(ok) != "" {
		t.Fatal("plain finished should have no error")
	}
}

func TestDecodePayload_HexFallback(t *testing.T) {
	ev := appendTextEvent("hx")
	raw, _ := proto.Marshal(ev)
	// hex-encode to exercise the fallback path
	hexStr := ""
	for _, b := range raw {
		const hexdigits = "0123456789abcdef"
		hexStr += string(hexdigits[b>>4]) + string(hexdigits[b&0xf])
	}
	got, _, ok, err := decodeSSELine("data: " + hexStr)
	if err != nil || !ok {
		t.Fatalf("hex decode err=%v ok=%v", err, ok)
	}
	if eventText(got) != "hx" {
		t.Fatalf("hex text = %q", eventText(got))
	}
}
