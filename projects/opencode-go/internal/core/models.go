package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// catalog is the baked-in fallback list of OpenCode Go model ids, verified live
// on 2026-07-19 from GET https://opencode.ai/zen/go/v1/models (22 ids). Used by
// model.static and as the model.for_auth fallback when live discovery fails.
var catalog = []string{
	"grok-4.5",
	"glm-5.2", "glm-5.1", "glm-5",
	"kimi-k3", "kimi-k2.7-code", "kimi-k2.6", "kimi-k2.5",
	"deepseek-v4-pro", "deepseek-v4-flash",
	"minimax-m3", "minimax-m2.7", "minimax-m2.5",
	"qwen3.7-max", "qwen3.7-plus", "qwen3.6-plus", "qwen3.5-plus",
	"mimo-v2-pro", "mimo-v2-omni", "mimo-v2.5-pro", "mimo-v2.5",
	"hy3-preview",
}

func modelInfo(id string) pluginapi.ModelInfo {
	return pluginapi.ModelInfo{
		ID:                         id,
		Object:                     "model",
		OwnedBy:                    ProviderKey,
		DisplayName:                id,
		SupportedGenerationMethods: []string{"chat"},
		UserDefined:                true,
	}
}

func staticModels() pluginapi.ModelResponse {
	models := make([]pluginapi.ModelInfo, 0, len(catalog))
	for _, id := range catalog {
		models = append(models, modelInfo(id))
	}
	return pluginapi.ModelResponse{Provider: ProviderKey, Models: models}
}

// openaiModelList is the shape of GET /models (OpenAI list).
type openaiModelList struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func modelsForAuth(request []byte, do HTTPDoer) ([]byte, error) {
	var req pluginapi.AuthModelRequest
	if len(request) > 0 {
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, fmt.Errorf("decode model.for_auth request: %w", err)
		}
	}
	resp, err := discoverModels(req, do)
	if err != nil {
		// Resilient fallback: still expose the baked-in catalogue.
		resp = staticModels()
	}
	return okEnvelope(resp)
}

func discoverModels(req pluginapi.AuthModelRequest, do HTTPDoer) (pluginapi.ModelResponse, error) {
	if do == nil {
		return pluginapi.ModelResponse{}, fmt.Errorf("no host HTTP bridge")
	}
	apiKey := strings.TrimSpace(req.Attributes["api_key"])
	if apiKey == "" {
		return pluginapi.ModelResponse{}, fmt.Errorf("missing api_key attribute")
	}
	baseURL := strings.TrimSpace(req.Attributes["base_url"])
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	httpResp, err := do(pluginapi.HTTPRequest{
		Method:  "GET",
		URL:     strings.TrimRight(baseURL, "/") + "/models",
		Headers: http.Header{"Authorization": {"Bearer " + apiKey}},
	})
	if err != nil {
		return pluginapi.ModelResponse{}, err
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return pluginapi.ModelResponse{}, fmt.Errorf("GET /models returned status %d", httpResp.StatusCode)
	}
	var list openaiModelList
	if err := json.Unmarshal(httpResp.Body, &list); err != nil {
		return pluginapi.ModelResponse{}, fmt.Errorf("decode /models body: %w", err)
	}
	models := make([]pluginapi.ModelInfo, 0, len(list.Data))
	for _, m := range list.Data {
		if strings.TrimSpace(m.ID) == "" {
			continue
		}
		models = append(models, modelInfo(m.ID))
	}
	if len(models) == 0 {
		return pluginapi.ModelResponse{}, fmt.Errorf("/models returned no usable ids")
	}
	return pluginapi.ModelResponse{Provider: ProviderKey, Models: models}, nil
}
