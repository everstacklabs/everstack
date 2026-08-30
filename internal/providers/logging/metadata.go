package logging

import (
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// ProviderMetadata contains standardized metadata extracted from provider responses
type ProviderMetadata struct {
	ResponseID       string
	ResponseText     string // First N chars for logging
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	FinishReason     string
	Model            string
}

// StreamMetadata contains metadata collected during streaming responses
type StreamMetadata struct {
	ResponseID       string
	FirstChunkText   string
	TotalChunks      int
	Model            string
	FinishReason     string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// MetadataExtractor defines the interface for extracting provider-specific metadata
type MetadataExtractor interface {
	ExtractFromResponse(response gw.ChatCompletionResponse) ProviderMetadata
	ExtractFromStreamChunk(chunk gw.ChatResponseChunk) StreamMetadata
}

// RequestMetadata contains metadata about the request
type RequestMetadata struct {
	Model     string
	Endpoint  string
	Stream    bool
	UserInput string
}

// ExtractRequestMetadata extracts metadata from the request
func ExtractRequestMetadata(req gw.ChatCompletionRequest, endpoint string) RequestMetadata {
	var userInput string

	// Latest user turn — see middleware.go extractUserInput for rationale.
	// The chat client sends the full transcript; we want the most recent
	// user message, not the oldest.
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role != gw.RoleUser || len(msg.Content) == 0 {
			continue
		}
		for _, content := range msg.Content {
			if content.Type == "text" && content.Text != nil && *content.Text != "" {
				userInput = *content.Text
				break
			}
		}
		if userInput != "" {
			break
		}
	}
	if len(userInput) > 500 {
		userInput = userInput[:500] + "..."
	}

	return RequestMetadata{
		Model:     req.Model,
		Endpoint:  endpoint,
		Stream:    req.Stream,
		UserInput: userInput,
	}
}

// TruncateText truncates text to a maximum length for logging
func TruncateText(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen] + "..."
}
