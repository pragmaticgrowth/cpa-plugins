package core

import (
	"testing"

	warppb "github.com/warpdotdev/warp-proto-apis/apis/multi_agent/v1/gen/go"
	"google.golang.org/protobuf/proto"
)

func TestSpike_BuildAndRoundTripRequest(t *testing.T) {
	req := warppb.Request_builder{
		Settings: warppb.Request_Settings_builder{
			ModelConfig: warppb.Request_Settings_ModelConfig_builder{
				Base: proto.String("claude-4-sonnet"),
			}.Build(),
			ApiKeys: warppb.Request_Settings_ApiKeys_builder{
				AllowUseOfWarpCredits: proto.Bool(true),
			}.Build(),
		}.Build(),
		Input: warppb.Request_Input_builder{
			UserInputs: warppb.Request_Input_UserInputs_builder{
				Inputs: []*warppb.Request_Input_UserInputs_UserInput{
					warppb.Request_Input_UserInputs_UserInput_builder{
						UserQuery: warppb.Request_Input_UserQuery_builder{
							Query: proto.String("hello"),
							ReferencedAttachments: map[string]*warppb.Attachment{
								"SYSTEM_PROMPT": warppb.Attachment_builder{
									PlainText: proto.String("be terse"),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				},
			}.Build(),
		}.Build(),
		TaskContext: warppb.Request_TaskContext_builder{
			Tasks: []*warppb.Task{
				warppb.Task_builder{
					Id: proto.String("task-0"),
					Messages: []*warppb.Message{
						warppb.Message_builder{
							Id:        proto.String("m-0"),
							TaskId:    proto.String("task-0"),
							UserQuery: warppb.Message_UserQuery_builder{Query: proto.String("prior")}.Build(),
						}.Build(),
						warppb.Message_builder{
							Id:          proto.String("m-1"),
							TaskId:      proto.String("task-0"),
							AgentOutput: warppb.Message_AgentOutput_builder{Text: proto.String("prior reply")}.Build(),
						}.Build(),
					},
				}.Build(),
			},
		}.Build(),
	}.Build()

	raw, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var got warppb.Request
	if err := proto.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if base := got.GetSettings().GetModelConfig().GetBase(); base != "claude-4-sonnet" {
		t.Fatalf("round-trip base = %q", base)
	}
	if !got.GetSettings().GetApiKeys().GetAllowUseOfWarpCredits() {
		t.Fatal("warp credits flag lost in round-trip")
	}
	inputs := got.GetInput().GetUserInputs().GetInputs()
	if len(inputs) != 1 || inputs[0].GetUserQuery().GetQuery() != "hello" {
		t.Fatalf("current query wrong: %+v", inputs)
	}
	if inputs[0].GetUserQuery().GetReferencedAttachments()["SYSTEM_PROMPT"].GetPlainText() != "be terse" {
		t.Fatal("system prompt attachment lost")
	}
	tasks := got.GetTaskContext().GetTasks()
	if len(tasks) != 1 || len(tasks[0].GetMessages()) != 2 {
		t.Fatalf("history wrong")
	}
	if tasks[0].GetMessages()[1].GetAgentOutput().GetText() != "prior reply" {
		t.Fatal("history agent output lost")
	}
}

func TestSpike_DecodeResponseEvent(t *testing.T) {
	ev := warppb.ResponseEvent_builder{
		Init: warppb.ResponseEvent_StreamInit_builder{
			ConversationId: proto.String("conv-1"),
		}.Build(),
	}.Build()
	raw, _ := proto.Marshal(ev)
	var got warppb.ResponseEvent
	if err := proto.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.GetInit().GetConversationId() != "conv-1" {
		t.Fatal("init conversation id not decoded")
	}

	// A client_actions event carrying an append-to-message-content text delta.
	ev2 := warppb.ResponseEvent_builder{
		ClientActions: warppb.ResponseEvent_ClientActions_builder{
			Actions: []*warppb.ClientAction{
				warppb.ClientAction_builder{
					AppendToMessageContent: warppb.ClientAction_AppendToMessageContent_builder{
						Message: warppb.Message_builder{
							AgentOutput: warppb.Message_AgentOutput_builder{Text: proto.String("Hello")}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			},
		}.Build(),
	}.Build()
	raw2, _ := proto.Marshal(ev2)
	var got2 warppb.ResponseEvent
	if err := proto.Unmarshal(raw2, &got2); err != nil {
		t.Fatal(err)
	}
	txt := got2.GetClientActions().GetActions()[0].GetAppendToMessageContent().GetMessage().GetAgentOutput().GetText()
	if txt != "Hello" {
		t.Fatalf("append text = %q", txt)
	}

	// A finished event with the max-token-limit reason arm set.
	fin := warppb.ResponseEvent_builder{
		Finished: warppb.ResponseEvent_StreamFinished_builder{
			MaxTokenLimit: warppb.ResponseEvent_StreamFinished_ReachedMaxTokenLimit_builder{}.Build(),
		}.Build(),
	}.Build()
	if fin.GetFinished().GetMaxTokenLimit() == nil {
		t.Fatal("max token limit arm not set")
	}
}
