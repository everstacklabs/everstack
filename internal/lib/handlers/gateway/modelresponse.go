package gateway

// Minimal helpers to construct normalized responses.

func NewChatResponse(id, model string, choices []Choice, usage Usage) ChatCompletionResponse {
	return ChatCompletionResponse{
		ID:      id,
		Created: nowUTC(),
		Model:   model,
		Choices: choices,
		Usage:   usage,
	}
}

func NewChatChunk(id, model string, deltas []ChoiceDelta) ChatResponseChunk {
	return ChatResponseChunk{
		ID:      id,
		Created: nowUTC(),
		Model:   model,
		Choices: deltas,
	}
}
