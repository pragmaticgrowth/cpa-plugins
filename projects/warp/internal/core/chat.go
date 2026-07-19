package core

import "encoding/json"

// ChatMessage is one message in a chat-completions request. v1 assumes string
// content (no content-part arrays).
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is the subset of a chat-completions request the plugin consumes.
type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

func parseChatRequest(raw []byte) (ChatRequest, error) {
	var cr ChatRequest
	if err := json.Unmarshal(raw, &cr); err != nil {
		return ChatRequest{}, err
	}
	return cr, nil
}
