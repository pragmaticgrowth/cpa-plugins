package core

import (
	"encoding/json"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// warpModelIDs is the curated v1 catalogue (Warp's server-side model IDs).
var warpModelIDs = []string{
	"auto",
	"claude-4.1-opus", "claude-4-opus", "claude-4-sonnet", "claude-4.5-sonnet",
	"gpt-5", "gpt-5 (high reasoning)", "gpt-4.1", "gpt-4o", "o3",
	"gemini-2.5-pro",
}

func prefixedModelID(id string) string { return CurrentConfig().ModelPrefix + id }

func stripModelPrefix(id string) string {
	p := CurrentConfig().ModelPrefix
	if p == "" {
		return id
	}
	return strings.TrimPrefix(id, p)
}

func handleModelRegister(raw []byte) (json.RawMessage, error) {
	models := make([]pluginapi.ModelInfo, 0, len(warpModelIDs))
	for _, id := range warpModelIDs {
		models = append(models, pluginapi.ModelInfo{
			ID:                         prefixedModelID(id),
			Object:                     "model",
			OwnedBy:                    "warp",
			DisplayName:                "Warp: " + id,
			SupportedGenerationMethods: []string{"chat"},
			ContextLength:              200000,
			UserDefined:                true,
		})
	}
	return okEnvelope(pluginapi.ModelRegistrationResponse{Provider: "warp", Models: models})
}
