package core

import (
	"encoding/json"
	"fmt"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// HostBridge is the subset of host callbacks the plugin needs. main.go provides
// the concrete implementation over the C ABI host-callback table.
type HostBridge interface {
	StreamEmit(streamID string, payload []byte) error
	StreamClose(streamID, errMsg string) error
	Log(level, msg string)
}

func okEnvelope(result any) (json.RawMessage, error) {
	body, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	env, err := json.Marshal(pluginabi.Envelope{OK: true, Result: body})
	if err != nil {
		return nil, err
	}
	return env, nil
}

// Dispatch routes an RPC method to its handler and returns a marshaled envelope.
func Dispatch(method string, request []byte, host HostBridge) (json.RawMessage, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		reg, err := handleLifecycle(request)
		if err != nil {
			return nil, err
		}
		return okEnvelope(json.RawMessage(reg))
	case pluginabi.MethodPluginShutdown:
		return okEnvelope(map[string]any{})

	case pluginabi.MethodAuthIdentifier:
		return authIdentifier()
	case pluginabi.MethodAuthParse:
		return handleAuthParse(request)
	case pluginabi.MethodAuthRefresh:
		return handleAuthRefresh(request)
	case pluginabi.MethodAuthLoginStart:
		return handleAuthLoginStart(request)
	case pluginabi.MethodAuthLoginPoll:
		return handleAuthLoginPoll(request)

	case pluginabi.MethodCommandLineRegister:
		return handleCLIRegister(request)
	case pluginabi.MethodCommandLineExecute:
		return handleCLIExecute(request)

	case pluginabi.MethodModelRegister:
		return handleModelRegister(request)

	case pluginabi.MethodExecutorIdentifier:
		return executorIdentifier()
	case pluginabi.MethodExecutorExecute:
		return handleExecute(request)
	case pluginabi.MethodExecutorExecuteStream:
		return handleExecuteStream(request, host)
	case pluginabi.MethodExecutorCountTokens:
		return handleCountTokens(request)
	case pluginabi.MethodExecutorHTTPRequest:
		return okEnvelope(pluginapi.ExecutorResponse{}) // v1 no-op

	default:
		return nil, fmt.Errorf("unknown method %q", method)
	}
}
