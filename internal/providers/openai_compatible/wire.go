package openai_compatible

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// This file holds the wire helpers shared by the OpenAI-compatible adapters
// (this package, deepseek, mistral, qwen, ollama), so each adapter does not
// re-implement multimodal content, token caps and upstream error handling.

// maxErrorBody bounds how much of an upstream error body is read.
const maxErrorBody = 1 << 20

// WireContent converts message parts to the OpenAI chat "content" value.
//
// A message with only text parts stays a plain string, because some
// OpenAI-compatible servers reject the array form. Any other part (images)
// switches the message to the content-parts array so image_url parts reach the
// model. Parts the OpenAI wire format cannot express (file ids, tool results)
// are dropped.
func WireContent(parts []gw.ContentPart) any {
	var text strings.Builder
	arrayParts := make([]map[string]any, 0, len(parts))
	multimodal := false
	for _, c := range parts {
		switch {
		case c.Type == "text" && c.Text != nil:
			text.WriteString(*c.Text)
			arrayParts = append(arrayParts, map[string]any{"type": "text", "text": *c.Text})
		case c.Type == "image_url" && c.ImageURL != nil:
			multimodal = true
			arrayParts = append(arrayParts, map[string]any{
				"type":      "image_url",
				"image_url": map[string]string{"url": *c.ImageURL},
			})
		}
	}
	if !multimodal {
		return text.String()
	}
	return arrayParts
}

// MaxOutputTokens returns the output cap to send as max_tokens. These
// upstreams generally do not accept max_completion_tokens, so the explicit
// completion cap is sent under the max_tokens name and wins when both are set.
func MaxOutputTokens(s gw.SamplingParams) int {
	if s.MaxCompletionTokens > 0 {
		return s.MaxCompletionTokens
	}
	if s.MaxTokens > 0 {
		return s.MaxTokens
	}
	return 0
}

// UpstreamError reads a non-2xx upstream response into a typed error that
// carries the upstream HTTP status, so the gateway can answer with a matching
// 4xx instead of an opaque 500. The caller still owns closing resp.Body.
func UpstreamError(provider string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	return gw.NewUpstreamError(provider, resp.StatusCode, body)
}

// ErrStreamTruncated reports a stream that ended without a finish marker.
func ErrStreamTruncated(provider string) error {
	return fmt.Errorf("%s stream ended before a finish_reason or [DONE]: %w", provider, io.ErrUnexpectedEOF)
}

// CachedPromptDetails builds prompt token details for a cached-token count.
// It returns nil when nothing was cached.
func CachedPromptDetails(cached int) *gw.TokenDetails {
	if cached <= 0 {
		return nil
	}
	return &gw.TokenDetails{CachedTokens: cached, CacheReadTokens: cached}
}

// NonEmptyEmbedding errors when an upstream returned no embedding vector. An
// empty vector would otherwise be stored by callers as a valid embedding.
func NonEmptyEmbedding(provider string, vec []float64) ([]float64, error) {
	if len(vec) == 0 {
		return nil, fmt.Errorf("%s returned an empty embedding", provider)
	}
	return vec, nil
}
