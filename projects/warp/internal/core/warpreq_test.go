package core

import (
	"testing"

	warppb "github.com/warpdotdev/warp-proto-apis/apis/multi_agent/v1/gen/go"
	"google.golang.org/protobuf/proto"
)

func TestBuildWarpRequest_MapsFields(t *testing.T) {
	_ = applyConfigYAML(nil)
	cr := ChatRequest{
		Model: "warp/claude-4-sonnet",
		Messages: []ChatMessage{
			{Role: "system", Content: "be terse"},
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
			{Role: "user", Content: "2+2?"},
		},
	}
	raw, err := BuildWarpRequest(CurrentConfig(), cr)
	if err != nil {
		t.Fatal(err)
	}
	var req warppb.Request
	if err := proto.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	if got := req.GetSettings().GetModelConfig().GetBase(); got != "claude-4-sonnet" {
		t.Fatalf("model base = %q (prefix not stripped?)", got)
	}
	if !req.GetSettings().GetApiKeys().GetAllowUseOfWarpCredits() {
		t.Fatal("warp credits flag not set")
	}
	inputs := req.GetInput().GetUserInputs().GetInputs()
	if len(inputs) != 1 || inputs[0].GetUserQuery().GetQuery() != "2+2?" {
		t.Fatalf("current turn wrong: %+v", inputs)
	}
	tasks := req.GetTaskContext().GetTasks()
	if len(tasks) != 1 || len(tasks[0].GetMessages()) != 2 {
		t.Fatalf("history wrong: %d tasks", len(tasks))
	}
	if tasks[0].GetMessages()[0].GetUserQuery().GetQuery() != "hi" {
		t.Fatal("history user turn wrong")
	}
	if tasks[0].GetMessages()[1].GetAgentOutput().GetText() != "hello" {
		t.Fatal("history assistant turn wrong")
	}
	att := inputs[0].GetUserQuery().GetReferencedAttachments()
	if att["SYSTEM_PROMPT"].GetPlainText() != "be terse" {
		t.Fatal("system prompt not folded")
	}
}

func TestBuildWarpRequest_SingleTurnNoHistory(t *testing.T) {
	_ = applyConfigYAML(nil)
	cr := ChatRequest{
		Model:    "warp/gpt-5",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}
	raw, err := BuildWarpRequest(CurrentConfig(), cr)
	if err != nil {
		t.Fatal(err)
	}
	var req warppb.Request
	if err := proto.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.GetTaskContext().GetTasks()) != 0 {
		t.Fatal("expected no history tasks for a single turn")
	}
	if req.GetInput().GetUserInputs().GetInputs()[0].GetUserQuery().GetQuery() != "hi" {
		t.Fatal("current query wrong")
	}
}

func TestBuildWarpRequest_Empty(t *testing.T) {
	if _, err := BuildWarpRequest(CurrentConfig(), ChatRequest{}); err == nil {
		t.Fatal("expected error for empty messages")
	}
}
