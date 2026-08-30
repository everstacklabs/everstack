package logging

import (
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// DefaultExtractor works for OpenAI-compatible response formats
type DefaultExtractor struct{}

func (e *DefaultExtractor) ExtractFromResponse(response gw.ChatCompletionResponse) ProviderMetadata {
	var responseText string
	var finishReason string

	if len(response.Choices) > 0 {
		choice := response.Choices[0]
		finishReason = choice.FinishReason

		// Extract text from message content
		if len(choice.Message.Content) > 0 {
			for _, content := range choice.Message.Content {
				if content.Type == "text" && content.Text != nil {
					responseText = *content.Text
					break
				}
			}
		}
	}

	return ProviderMetadata{
		ResponseID:       response.ID,
		ResponseText:     TruncateText(responseText, 500),
		PromptTokens:     response.Usage.PromptTokens,
		CompletionTokens: response.Usage.CompletionTokens,
		TotalTokens:      response.Usage.TotalTokens,
		FinishReason:     finishReason,
		Model:            response.Model,
	}
}

func (e *DefaultExtractor) ExtractFromStreamChunk(chunk gw.ChatResponseChunk) StreamMetadata {
	var firstText string
	var finishReason string

	if len(chunk.Choices) > 0 {
		delta := chunk.Choices[0]
		finishReason = delta.FinishReason

		if len(delta.Delta.Content) > 0 {
			for _, content := range delta.Delta.Content {
				if content.Type == "text" && content.Text != nil {
					firstText = *content.Text
					break
				}
			}
		}
	}

	// Extract usage if present (typically in final chunk)
	var promptTokens, completionTokens, totalTokens int
	if chunk.Usage != nil {
		promptTokens = chunk.Usage.PromptTokens
		completionTokens = chunk.Usage.CompletionTokens
		totalTokens = chunk.Usage.TotalTokens
	}

	return StreamMetadata{
		ResponseID:       chunk.ID,
		FirstChunkText:   TruncateText(firstText, 200),
		TotalChunks:      1,
		Model:            chunk.Model,
		FinishReason:     finishReason,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
	}
}

// AnthropicExtractor handles Claude-specific response format
type AnthropicExtractor struct{}

func (e *AnthropicExtractor) ExtractFromResponse(response gw.ChatCompletionResponse) ProviderMetadata {
	return (&DefaultExtractor{}).ExtractFromResponse(response)
}

func (e *AnthropicExtractor) ExtractFromStreamChunk(chunk gw.ChatResponseChunk) StreamMetadata {
	return (&DefaultExtractor{}).ExtractFromStreamChunk(chunk)
}

// CohereExtractor handles Cohere-specific response format
type CohereExtractor struct{}

func (e *CohereExtractor) ExtractFromResponse(response gw.ChatCompletionResponse) ProviderMetadata {
	return (&DefaultExtractor{}).ExtractFromResponse(response)
}

func (e *CohereExtractor) ExtractFromStreamChunk(chunk gw.ChatResponseChunk) StreamMetadata {
	return (&DefaultExtractor{}).ExtractFromStreamChunk(chunk)
}

// GoogleExtractor handles Google/Gemini-specific response format
type GoogleExtractor struct{}

func (e *GoogleExtractor) ExtractFromResponse(response gw.ChatCompletionResponse) ProviderMetadata {
	return (&DefaultExtractor{}).ExtractFromResponse(response)
}

func (e *GoogleExtractor) ExtractFromStreamChunk(chunk gw.ChatResponseChunk) StreamMetadata {
	return (&DefaultExtractor{}).ExtractFromStreamChunk(chunk)
}

// SelectExtractor returns the appropriate extractor for a provider
func SelectExtractor(providerName string) MetadataExtractor {
	switch providerName {
	case "anthropic":
		return &AnthropicExtractor{}
	case "cohere":
		return &CohereExtractor{}
	case "google":
		return &GoogleExtractor{}
	default:
		return &DefaultExtractor{}
	}
}
