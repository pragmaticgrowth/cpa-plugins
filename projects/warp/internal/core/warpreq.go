package core

import (
	"fmt"
	"strings"

	warppb "github.com/warpdotdev/warp-proto-apis/apis/multi_agent/v1/gen/go"
	"google.golang.org/protobuf/proto"
)

// BuildWarpRequest maps a chat-completions request to a marshaled Warp Request.
//
// Layout (stateless — full history inline each call):
//   - system messages   -> folded into the final UserQuery.referenced_attachments["SYSTEM_PROMPT"]
//   - all but last turn  -> task_context.tasks[0].messages[]
//   - last turn          -> input.user_inputs.inputs[0].user_query
//   - settings.model_config.base = stripped model; api_keys.allow_use_of_warp_credits = cfg
func BuildWarpRequest(cfg Config, cr ChatRequest) ([]byte, error) {
	if len(cr.Messages) == 0 {
		return nil, fmt.Errorf("no messages")
	}
	model := stripModelPrefix(cr.Model)
	if model == "" {
		model = "auto"
	}

	var systemParts []string
	for _, m := range cr.Messages {
		if m.Role == "system" && m.Content != "" {
			systemParts = append(systemParts, m.Content)
		}
	}
	nonSystem := filterNonSystem(cr.Messages)
	if len(nonSystem) == 0 {
		return nil, fmt.Errorf("no user/assistant messages")
	}
	last := nonSystem[len(nonSystem)-1]
	history := nonSystem[:len(nonSystem)-1]

	// history -> task_context.tasks[0].messages[]
	taskID := "task-0"
	histMsgs := make([]*warppb.Message, 0, len(history))
	for i, m := range history {
		if msg := historyMessage(taskID, fmt.Sprintf("m-%d", i), m); msg != nil {
			histMsgs = append(histMsgs, msg)
		}
	}

	// current turn -> input.user_inputs.inputs[0].user_query
	uq := warppb.Request_Input_UserQuery_builder{Query: proto.String(last.Content)}
	if len(systemParts) > 0 {
		uq.ReferencedAttachments = map[string]*warppb.Attachment{
			"SYSTEM_PROMPT": warppb.Attachment_builder{
				PlainText: proto.String(strings.Join(systemParts, "\n\n")),
			}.Build(),
		}
	}

	reqBuilder := warppb.Request_builder{
		Input: warppb.Request_Input_builder{
			UserInputs: warppb.Request_Input_UserInputs_builder{
				Inputs: []*warppb.Request_Input_UserInputs_UserInput{
					warppb.Request_Input_UserInputs_UserInput_builder{UserQuery: uq.Build()}.Build(),
				},
			}.Build(),
		}.Build(),
		Settings: warppb.Request_Settings_builder{
			ModelConfig: warppb.Request_Settings_ModelConfig_builder{Base: proto.String(model)}.Build(),
			ApiKeys:     warppb.Request_Settings_ApiKeys_builder{AllowUseOfWarpCredits: proto.Bool(cfg.UseWarpCredits)}.Build(),
		}.Build(),
	}
	if len(histMsgs) > 0 {
		reqBuilder.TaskContext = warppb.Request_TaskContext_builder{
			Tasks: []*warppb.Task{
				warppb.Task_builder{Id: proto.String(taskID), Messages: histMsgs}.Build(),
			},
		}.Build()
	}

	return proto.Marshal(reqBuilder.Build())
}

func historyMessage(taskID, id string, m ChatMessage) *warppb.Message {
	switch m.Role {
	case "user":
		return warppb.Message_builder{
			Id: proto.String(id), TaskId: proto.String(taskID),
			UserQuery: warppb.Message_UserQuery_builder{Query: proto.String(m.Content)}.Build(),
		}.Build()
	case "assistant":
		return warppb.Message_builder{
			Id: proto.String(id), TaskId: proto.String(taskID),
			AgentOutput: warppb.Message_AgentOutput_builder{Text: proto.String(m.Content)}.Build(),
		}.Build()
	}
	return nil
}

func filterNonSystem(msgs []ChatMessage) []ChatMessage {
	out := make([]ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		if m.Role != "system" {
			out = append(out, m)
		}
	}
	return out
}
