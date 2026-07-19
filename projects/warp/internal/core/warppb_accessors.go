package core

// This file records the CONFIRMED Warp multi_agent v1 protobuf accessor names,
// verified in Task 2 against the generated package
//   github.com/warpdotdev/warp-proto-apis/apis/multi_agent/v1/gen/go
// (protoc-gen-go v1.36.6, Editions-2023 / Opaque API, module pseudo-version
//  v0.0.0-20260718194153-ea825ae5f3ed). The generated Go package is named `v1`;
// callers import it as `warppb "…/multi_agent/v1/gen/go"`.
//
// Opaque API: construct with <Type>_builder{...}.Build(); read with Get<Field>().
// Use google.golang.org/protobuf/proto for proto.String/Bool, Marshal, Unmarshal.
//
// REQUEST SIDE (verified):
//   warppb.Request_builder{TaskContext, Input, Settings, Metadata}.Build()
//   warppb.Request_TaskContext_builder{Tasks []*warppb.Task}
//   warppb.Request_Input_builder{UserInputs *warppb.Request_Input_UserInputs}   // oneof arm
//   warppb.Request_Input_UserInputs_builder{Inputs []*warppb.Request_Input_UserInputs_UserInput}
//   warppb.Request_Input_UserInputs_UserInput_builder{UserQuery *warppb.Request_Input_UserQuery} // oneof arm
//   warppb.Request_Input_UserQuery_builder{Query *string, ReferencedAttachments map[string]*warppb.Attachment}
//   warppb.Request_Settings_builder{ModelConfig *warppb.Request_Settings_ModelConfig, ApiKeys *warppb.Request_Settings_ApiKeys}
//   warppb.Request_Settings_ModelConfig_builder{Base *string}
//   warppb.Request_Settings_ApiKeys_builder{AllowUseOfWarpCredits *bool}
//   warppb.Request_Metadata_builder{ConversationId *string}
//   warppb.Attachment_builder{PlainText *string}                               // oneof value arm
//   warppb.Task_builder{Id *string, Messages []*warppb.Message}
//   warppb.Message_builder{Id, TaskId *string, UserQuery *warppb.Message_UserQuery, AgentOutput *warppb.Message_AgentOutput} // oneof arms
//   warppb.Message_UserQuery_builder{Query *string}
//   warppb.Message_AgentOutput_builder{Text *string}
//
// RESPONSE SIDE (verified):
//   warppb.ResponseEvent_builder{Init *StreamInit, ClientActions *ClientActions, Finished *StreamFinished} // oneof arms
//   warppb.ResponseEvent_StreamInit_builder{ConversationId, RequestId, RunId *string}
//   warppb.ResponseEvent_ClientActions_builder{Actions []*warppb.ClientAction}
//   warppb.ResponseEvent_StreamFinished_builder{Done, MaxTokenLimit, QuotaLimit, LlmUnavailable, InternalError, InvalidApiKey, Other, ContextWindowExceeded ...} // oneof reason arms
//   warppb.ClientAction_builder{AppendToMessageContent *..., AddMessagesToTask *..., ...} // oneof action arms
//   warppb.ClientAction_AppendToMessageContent_builder{Message *warppb.Message, TaskId *string}
//   warppb.ClientAction_AddMessagesToTask_builder{Messages []*warppb.Message, TaskId *string}
//
// GETTERS (verified):
//   ev.GetInit(), ev.GetClientActions(), ev.GetFinished()
//   ev.GetInit().GetConversationId()
//   ev.GetClientActions().GetActions()  -> []*ClientAction
//   a.GetAppendToMessageContent(), a.GetAddMessagesToTask()
//   a.GetAppendToMessageContent().GetMessage().GetAgentOutput().GetText()
//   a.GetAddMessagesToTask().GetMessages() -> each .GetAgentOutput().GetText()
//   fin.GetMaxTokenLimit() (non-nil pointer means that oneof arm is set);
//     also GetDone(), GetQuotaLimit(), GetLlmUnavailable(), GetInternalError(), GetInvalidApiKey()
//   req.GetSettings().GetModelConfig().GetBase()
//   req.GetSettings().GetApiKeys().GetAllowUseOfWarpCredits()
//   req.GetInput().GetUserInputs().GetInputs()[0].GetUserQuery().GetQuery()
//   req.GetInput().GetUserInputs().GetInputs()[0].GetUserQuery().GetReferencedAttachments()
//   req.GetTaskContext().GetTasks()[0].GetMessages()
//
// All names above match the implementation-plan guesses verbatim; no corrections
// were required.
