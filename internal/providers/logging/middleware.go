package logging

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/database"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// LoggingMiddleware wraps a provider with structured logging
type LoggingMiddleware struct {
	provider  gw.Provider
	extractor MetadataExtractor
}

// NewMiddleware creates a new logging middleware wrapper
func NewMiddleware(provider gw.Provider, extractor MetadataExtractor) gw.Provider {
	return &LoggingMiddleware{
		provider:  provider,
		extractor: extractor,
	}
}

// Unwrap returns the inner provider, allowing capability discovery through middleware layers.
func (m *LoggingMiddleware) Unwrap() gw.Provider {
	return m.provider
}

// tenantField returns a "tenant_id" logrus field pair from context for OTEL log isolation.
// Returns nil if no tenant context is set (self-hosted mode).
//
// Reads TenantSchemaFromContext first for backwards compatibility, then
// falls back to contextkeys.GetTenantID. After PR #48 the two carry the
// same value, but auth paths that only set one (cookie session, api_key
// interceptor) used to leave logs without a tenant_id attribute, which
// silently dropped every operational log out of the dashboard's Logs
// view. Falling back here is defense-in-depth alongside the middleware
// fix that now sets both keys.
func tenantField(ctx context.Context) []interface{} {
	tid := database.TenantSchemaFromContext(ctx)
	if tid == "" {
		tid = contextkeys.GetTenantID(ctx)
	}
	if tid != "" {
		return []interface{}{"tenant_id", tid}
	}
	return nil
}

// withTenant returns a logger entry with tenant_id field set from context.
func withTenant(ctx context.Context, entry *logger.Entry) *logger.Entry {
	if f := tenantField(ctx); f != nil {
		return entry.SetFields(f...)
	}
	return entry
}

// Name returns the wrapped provider's name
func (m *LoggingMiddleware) Name() string {
	return m.provider.Name()
}

// SupportsModel delegates to the wrapped provider
func (m *LoggingMiddleware) SupportsModel(model string) bool {
	return m.provider.SupportsModel(model)
}

// Chat wraps the provider's Chat method with structured logging
func (m *LoggingMiddleware) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	start := time.Now()
	cid := correlation.GetCorrelationID(ctx)
	providerName := m.provider.Name()

	// Determine endpoint (provider-specific)
	endpoint := m.getEndpoint(providerName, req.Model)

	// Extract user input for logging
	userInput := extractUserInput(req)

	// Log provider request issued with user input
	requestPayload := m.buildRequestPayload(providerName, req.Model, endpoint, userInput, cid)

	withTenant(ctx, logger.WithCategory(logger.CategoryOperational).
		WithLogEvent(logger.EventProviderRequestIssued).
		WithPayload(requestPayload).
		SetFields(
			"provider", providerName,
			"endpoint", endpoint,
			"model", req.Model,
			"stream", false,
			"command_type", "ChatCompletion",
			"correlation_id", cid,
		)).Info("provider request issued")

	// Call the actual provider
	resp, err := m.provider.Chat(ctx, req)
	latencyMs := time.Since(start).Milliseconds()

	if err != nil {
		// Log provider error with full error details
		errPayload := m.buildErrorPayload(providerName, req.Model, endpoint, userInput, err, cid, latencyMs)

		withTenant(ctx, logger.WithCategory(logger.CategoryOperational).
			WithLogEvent(logger.EventProviderError).
			WithPayload(errPayload).
			SetFields(
				"provider", providerName,
				"error", err.Error(),
				"correlation_id", cid,
				"latency_ms", latencyMs,
			)).Error("provider request failed")

		return gw.ChatCompletionResponse{}, err
	}

	// Extract metadata from response
	metadata := m.extractor.ExtractFromResponse(resp)

	// Log provider response received
	responsePayload := m.buildResponsePayload(providerName, metadata, cid, latencyMs)

	withTenant(ctx, logger.WithCategory(logger.CategoryOperational).
		WithLogEvent(logger.EventProviderResponseReceived).
		WithPayload(responsePayload).
		SetFields(
			"provider", providerName,
			"correlation_id", cid,
			"command_type", "ChatCompletion",
			"latency_ms", latencyMs,
			"prompt_tokens", metadata.PromptTokens,
			"completion_tokens", metadata.CompletionTokens,
		)).Info("provider response received")

	return resp, nil
}

// chunkTiming stores timing data for a single chunk
type chunkTiming struct {
	Index            int   `json:"index"`
	TimestampMs      int64 `json:"timestamp_ms"`
	LatencyMs        int64 `json:"latency_ms"`
	TokenCount       int   `json:"token_count"`
	CumulativeTokens int   `json:"cumulative_tokens"`
}

// streamingMetricsData stores computed streaming performance metrics
type streamingMetricsData struct {
	TtftMs                 int64         `json:"ttft_ms"`
	ChunkCount             int           `json:"chunk_count"`
	AvgChunkLatencyMs      float64       `json:"avg_chunk_latency_ms"`
	MaxChunkLatencyMs      int64         `json:"max_chunk_latency_ms"`
	TokensPerSecond        float64       `json:"tokens_per_second"`
	StreamDurationMs       int64         `json:"stream_duration_ms"`
	PartialResponseOnError string        `json:"partial_response_on_error,omitempty"`
	ChunkTimeline          []chunkTiming `json:"chunk_timeline,omitempty"`
}

// maxChunkTimelineSize limits chunk timeline entries to prevent huge payloads
const maxChunkTimelineSize = 100

// estimateTokenCount provides a rough token estimate based on text length
func estimateTokenCount(text string) int {
	// Rough approximation: ~4 characters per token for English text
	count := len(text) / 4
	if count == 0 && len(text) > 0 {
		return 1
	}
	return count
}

// ChatStream wraps the provider's ChatStream method with structured logging
func (m *LoggingMiddleware) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	start := time.Now()
	cid := correlation.GetCorrelationID(ctx)
	providerName := m.provider.Name()

	// Determine endpoint
	endpoint := m.getEndpoint(providerName, req.Model)

	// Extract user input for logging
	userInput := extractUserInput(req)

	// Log provider request issued with user input
	requestPayload := m.buildRequestPayload(providerName, req.Model, endpoint, userInput, cid)

	withTenant(ctx, logger.WithCategory(logger.CategoryOperational).
		WithLogEvent(logger.EventProviderRequestIssued).
		WithPayload(requestPayload).
		SetFields(
			"provider", providerName,
			"endpoint", endpoint,
			"model", req.Model,
			"stream", true,
			"command_type", "ChatCompletion",
			"correlation_id", cid,
		)).Info("provider stream request issued")

	// Track streaming metadata and accumulate text
	var streamMeta StreamMetadata
	chunkCount := 0
	var responseBuilder strings.Builder

	// Streaming metrics tracking
	var firstChunkTime time.Time
	var lastChunkTime time.Time
	var chunkTimings []chunkTiming
	var totalInterChunkLatency int64
	var maxChunkLatency int64
	var cumulativeTokens int

	// Track tool calls seen in streaming chunks (for diagnostics)
	var streamedToolCallNames []string

	// Wrap the chunk callback to capture metadata and accumulate text
	wrappedCallback := func(chunk gw.ChatResponseChunk) error {
		now := time.Now()

		// Track first chunk for TTFT
		if chunkCount == 0 {
			firstChunkTime = now
			streamMeta = m.extractor.ExtractFromStreamChunk(chunk)
		}

		// Calculate inter-chunk latency (time since last chunk)
		var interChunkLatency int64
		if chunkCount > 0 {
			interChunkLatency = now.Sub(lastChunkTime).Milliseconds()
			totalInterChunkLatency += interChunkLatency
			if interChunkLatency > maxChunkLatency {
				maxChunkLatency = interChunkLatency
			}
		}
		lastChunkTime = now

		// Accumulate text and count tokens from all chunks
		chunkText := ""
		if len(chunk.Choices) > 0 {
			for _, content := range chunk.Choices[0].Delta.Content {
				if content.Type == "text" && content.Text != nil {
					chunkText += *content.Text
					responseBuilder.WriteString(*content.Text)
				}
			}

			// Track tool call names for diagnostics
			for _, tc := range chunk.Choices[0].Delta.ToolCalls {
				if tc.Function.Name != "" {
					streamedToolCallNames = append(streamedToolCallNames, tc.Function.Name)
				}
			}

			// Update finish reason from chunks
			if chunk.Choices[0].FinishReason != "" {
				streamMeta.FinishReason = chunk.Choices[0].FinishReason
			}
		}

		// Estimate tokens for this chunk
		chunkTokens := estimateTokenCount(chunkText)
		cumulativeTokens += chunkTokens

		// Record chunk timing (limit to maxChunkTimelineSize entries)
		if len(chunkTimings) < maxChunkTimelineSize {
			chunkTimings = append(chunkTimings, chunkTiming{
				Index:            chunkCount,
				TimestampMs:      now.UnixMilli(),
				LatencyMs:        interChunkLatency,
				TokenCount:       chunkTokens,
				CumulativeTokens: cumulativeTokens,
			})
		}

		chunkCount++

		// Update usage if present (typically in final chunk)
		if chunk.Usage != nil {
			streamMeta.PromptTokens = chunk.Usage.PromptTokens
			streamMeta.CompletionTokens = chunk.Usage.CompletionTokens
			streamMeta.TotalTokens = chunk.Usage.TotalTokens
		}

		return onChunk(chunk)
	}

	// Call the actual provider stream
	err := m.provider.ChatStream(ctx, req, wrappedCallback)
	latencyMs := time.Since(start).Milliseconds()

	// Compute streaming metrics
	fullResponseText := responseBuilder.String()
	streamMetrics := m.computeStreamingMetrics(
		start, firstChunkTime, lastChunkTime,
		chunkCount, totalInterChunkLatency, maxChunkLatency,
		streamMeta.CompletionTokens, cumulativeTokens,
		chunkTimings,
	)

	if err != nil {
		// Capture partial response for error diagnosis
		streamMetrics.PartialResponseOnError = fullResponseText

		// Log provider error with streaming metrics and partial response
		errPayload := m.buildStreamErrorPayload(providerName, req.Model, endpoint, userInput, err, cid, latencyMs, streamMetrics)

		withTenant(ctx, logger.WithCategory(logger.CategoryOperational).
			WithLogEvent(logger.EventProviderError).
			WithPayload(errPayload).
			SetFields(
				"provider", providerName,
				"error", err.Error(),
				"correlation_id", cid,
				"latency_ms", latencyMs,
				"chunks_received", chunkCount,
				"stream", true,
				"stream.ttft_ms", streamMetrics.TtftMs,
				"stream.chunk_count", streamMetrics.ChunkCount,
			)).Error("provider stream failed")

		return err
	}

	// Log stream completion with streaming metrics
	streamMeta.TotalChunks = chunkCount
	streamPayload := m.buildStreamResponsePayloadWithMetrics(providerName, streamMeta, cid, latencyMs, fullResponseText, streamMetrics)

	// Build tool call summary for logging
	toolCallSummary := ""
	if len(streamedToolCallNames) > 0 {
		toolCallSummary = strings.Join(streamedToolCallNames, ",")
	}

	withTenant(ctx, logger.WithCategory(logger.CategoryOperational).
		WithLogEvent(logger.EventProviderResponseReceived).
		WithPayload(streamPayload).
		SetFields(
			"provider", providerName,
			"correlation_id", cid,
			"command_type", "ChatCompletion",
			"latency_ms", latencyMs,
			"chunks_received", chunkCount,
			"stream", true,
			"prompt_tokens", streamMeta.PromptTokens,
			"completion_tokens", streamMeta.CompletionTokens,
			"tool_calls", toolCallSummary,
			"tool_call_count", len(streamedToolCallNames),
			"stream.ttft_ms", streamMetrics.TtftMs,
			"stream.chunk_count", streamMetrics.ChunkCount,
			"stream.avg_chunk_latency_ms", fmt.Sprintf("%.2f", streamMetrics.AvgChunkLatencyMs),
			"stream.max_chunk_latency_ms", streamMetrics.MaxChunkLatencyMs,
			"stream.tokens_per_second", fmt.Sprintf("%.2f", streamMetrics.TokensPerSecond),
			"stream.duration_ms", streamMetrics.StreamDurationMs,
		)).Info("provider stream completed")

	return nil
}

// computeStreamingMetrics calculates streaming performance metrics
func (m *LoggingMiddleware) computeStreamingMetrics(
	requestStart, firstChunk, lastChunk time.Time,
	chunkCount int, totalInterChunkLatency, maxChunkLatency int64,
	completionTokens, estimatedTokens int,
	chunkTimings []chunkTiming,
) streamingMetricsData {
	metrics := streamingMetricsData{
		ChunkCount:        chunkCount,
		MaxChunkLatencyMs: maxChunkLatency,
		ChunkTimeline:     chunkTimings,
	}

	// TTFT: time from request start to first chunk
	if !firstChunk.IsZero() {
		metrics.TtftMs = firstChunk.Sub(requestStart).Milliseconds()
	}

	// Stream duration: time from first chunk to last chunk
	if !firstChunk.IsZero() && !lastChunk.IsZero() {
		metrics.StreamDurationMs = lastChunk.Sub(firstChunk).Milliseconds()
	}

	// Average inter-chunk latency (exclude first chunk since it has no previous)
	if chunkCount > 1 {
		metrics.AvgChunkLatencyMs = float64(totalInterChunkLatency) / float64(chunkCount-1)
	}

	// Tokens per second calculation
	// Prefer actual completion tokens from usage if available, otherwise use estimated
	tokens := completionTokens
	if tokens == 0 {
		tokens = estimatedTokens
	}
	if metrics.StreamDurationMs > 0 && tokens > 0 {
		// Convert ms to seconds for tokens/sec
		durationSec := float64(metrics.StreamDurationMs) / 1000.0
		metrics.TokensPerSecond = float64(tokens) / durationSec
	}

	return metrics
}

// Embed wraps the provider's Embed method with structured logging
func (m *LoggingMiddleware) Embed(ctx context.Context, req gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	// Skip logging for internal calls (like semantic cache embedding generation)
	// These should not appear in the request journey logs
	if contextkeys.IsInternalCall(ctx) {
		return m.provider.Embed(ctx, req)
	}

	start := time.Now()
	cid := correlation.GetCorrelationID(ctx)
	providerName := m.provider.Name()

	// Truncate input for logging (max 500 chars like ChatCompletion)
	inputPreview := req.Input
	if len(inputPreview) > 500 {
		inputPreview = inputPreview[:500] + "..."
	}

	// Build request payload for log extraction (same structure as Chat)
	requestPayload := m.buildEmbeddingRequestPayload(providerName, req.Model, inputPreview, cid)

	// Log embedding request with clear input message
	withTenant(ctx, logger.WithCategory(logger.CategoryOperational).
		WithLogEvent(logger.EventProviderRequestIssued).
		WithPayload(requestPayload).
		SetFields(
			"correlation_id", cid,
			"provider", providerName,
			"model", req.Model,
			"command_type", "Embeddings",
			"input", inputPreview,
			"input_chars", len(req.Input),
		)).Info(fmt.Sprintf("[%s] Embedding request: \"%s\"", cid, truncateForLog(inputPreview, 100)))

	// Call the actual provider
	resp, err := m.provider.Embed(ctx, req)
	latencyMs := time.Since(start).Milliseconds()

	if err != nil {
		withTenant(ctx, logger.WithCategory(logger.CategoryOperational).
			WithLogEvent(logger.EventProviderError).
			SetFields(
				"correlation_id", cid,
				"provider", providerName,
				"model", req.Model,
				"input", inputPreview,
				"error", err.Error(),
				"latency_ms", latencyMs,
			)).Error(fmt.Sprintf("[%s] Embedding failed: %s", cid, err.Error()))

		return gw.EmbeddingsResponse{}, err
	}

	// Calculate embedding summary for meaningful logging
	embSummary := summarizeEmbedding(resp.Embedding)

	// Get token usage if available
	promptTokens := 0
	if resp.Usage != nil {
		promptTokens = resp.Usage.PromptTokens
	}

	// Create a meaningful output description
	outputDescription := formatEmbeddingOutput(embSummary)

	// Build response payload for log extraction
	responsePayload := m.buildEmbeddingResponsePayload(providerName, resp.Model, embSummary, promptTokens, cid, latencyMs)

	// Log embedding response with meaningful output description
	withTenant(ctx, logger.WithCategory(logger.CategoryOperational).
		WithLogEvent(logger.EventProviderResponseReceived).
		WithPayload(responsePayload).
		SetFields(
			"correlation_id", cid,
			"provider", providerName,
			"model", resp.Model,
			"command_type", "Embeddings",
			"latency_ms", latencyMs,
			"input", inputPreview,
			"output", outputDescription,
			"dimension", embSummary.Dimension,
			"magnitude", fmt.Sprintf("%.4f", embSummary.Magnitude),
			"normalized", embSummary.IsNormalized,
			"embedding_hash", embSummary.Hash,
			"prompt_tokens", promptTokens,
		)).Info(fmt.Sprintf("[%s] Embedding complete: %s → %s (%dms)", cid, truncateForLog(inputPreview, 50), outputDescription, latencyMs))

	return resp, nil
}

// truncateForLog truncates a string for inline log messages
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// formatEmbeddingOutput creates a human-readable description of the embedding
func formatEmbeddingOutput(summary embeddingSummary) string {
	normStatus := "unnormalized"
	if summary.IsNormalized {
		normStatus = "normalized"
	}
	return fmt.Sprintf("%d-dim vector (%s, magnitude=%.4f)", summary.Dimension, normStatus, summary.Magnitude)
}

// embeddingSummary contains meaningful metrics about an embedding vector
type embeddingSummary struct {
	Dimension    int       `json:"dimension"`
	Magnitude    float64   `json:"magnitude"`
	IsNormalized bool      `json:"is_normalized"`
	FirstValues  []float64 `json:"first_values"`
	Hash         string    `json:"hash"`
	MinValue     float64   `json:"min_value"`
	MaxValue     float64   `json:"max_value"`
	MeanValue    float64   `json:"mean_value"`
}

// summarizeEmbedding creates a meaningful summary of an embedding vector
func summarizeEmbedding(embedding []float64) embeddingSummary {
	summary := embeddingSummary{
		Dimension:   len(embedding),
		FirstValues: make([]float64, 0, 5),
	}

	if len(embedding) == 0 {
		return summary
	}

	// Calculate statistics
	var sumSquares float64
	var sum float64
	minVal := embedding[0]
	maxVal := embedding[0]

	for _, v := range embedding {
		sumSquares += v * v
		sum += v
		if v < minVal {
			minVal = v
		}
		if v > maxVal {
			maxVal = v
		}
	}

	// L2 norm (magnitude)
	summary.Magnitude = math.Sqrt(sumSquares)

	// Check if normalized (magnitude ≈ 1.0, within tolerance)
	summary.IsNormalized = math.Abs(summary.Magnitude-1.0) < 0.01

	// Statistics
	summary.MinValue = math.Round(minVal*10000) / 10000
	summary.MaxValue = math.Round(maxVal*10000) / 10000
	summary.MeanValue = math.Round((sum/float64(len(embedding)))*10000) / 10000

	// Get first 5 values
	count := 5
	if len(embedding) < count {
		count = len(embedding)
	}
	for i := 0; i < count; i++ {
		// Round to 6 decimal places for readability
		summary.FirstValues = append(summary.FirstValues, math.Round(embedding[i]*1000000)/1000000)
	}

	// Calculate SHA256 hash of embedding for cache debugging
	h := sha256.New()
	for _, v := range embedding {
		h.Write([]byte(fmt.Sprintf("%.10f", v)))
	}
	summary.Hash = hex.EncodeToString(h.Sum(nil))[:16] // First 16 chars

	return summary
}

// extractUserInput extracts the user's input text from the request.
//
// Walks the messages array IN REVERSE so we capture the LATEST user
// turn — the one that triggered this provider call. The earlier
// implementation took the first user message, which in a multi-turn
// conversation is the OLDEST one in history. That's why the Logs UI
// showed an unrelated old message ("who is Trump") for a request
// whose latest turn was actually "hi" — the chat client sends the
// full transcript every turn, so req.Messages contains every prior
// user message; the right "input" for tracing is the most recent.
//
// No truncation: per the no-truncation retention policy
// (docs/telemetry/RETENTION_POLICY.md) we store the full input
// payload. A previous version capped at 500 chars + "…" which made
// non-trivial prompts useless in the logs sheet. Per-tier IO caps
// land at the ingest layer when we wire the retention policy
// enforcement — not here.
//
// We do keep a very high safety belt at 100 KiB to avoid pathological
// inputs (binary blobs accidentally pasted into a message). 100 KiB
// is roughly 25 KB of text, well above any real chat prompt.
func extractUserInput(req gw.ChatCompletionRequest) string {
	var userInput string

	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role != gw.RoleUser || len(msg.Content) == 0 {
			continue
		}
		// Concatenate every text part on the message so multi-part
		// content (e.g. text + image parts where the text holds the
		// prompt) doesn't get clipped to just the first part.
		var parts []string
		for _, content := range msg.Content {
			if content.Type == "text" && content.Text != nil && *content.Text != "" {
				parts = append(parts, *content.Text)
			}
		}
		if len(parts) > 0 {
			userInput = strings.Join(parts, "\n")
			break
		}
	}

	const safetyBeltBytes = 100 * 1024
	if len(userInput) > safetyBeltBytes {
		userInput = userInput[:safetyBeltBytes] + "… [truncated by safety belt]"
	}
	return userInput
}

// getEndpoint returns a reasonable endpoint string for logging
func (m *LoggingMiddleware) getEndpoint(providerName, model string) string {
	switch providerName {
	case "anthropic":
		return "https://api.anthropic.com/v1/messages"
	case "openai":
		if strings.Contains(strings.ToLower(model), "-codex") {
			return "https://api.openai.com/v1/responses"
		}
		return "https://api.openai.com/v1/chat/completions"
	case "cohere":
		return "https://api.cohere.ai/v2/chat"
	case "google":
		return "https://generativelanguage.googleapis.com/v1beta/models/" + model + ":generateContent"
	case "mistral":
		return "https://api.mistral.ai/v1/chat/completions"
	default:
		return providerName + "/chat/completions"
	}
}

// buildRequestPayload creates a structured payload for request logging
func (m *LoggingMiddleware) buildRequestPayload(providerName, model, endpoint, userInput, cid string) string {
	payloadData := map[string]interface{}{
		"provider": map[string]interface{}{
			"gateway.provider.name":     providerName,
			"gateway.provider.model":    model,
			"gateway.provider.endpoint": endpoint,
		},
		"request": map[string]interface{}{
			"user_input": userInput,
		},
		"correlation": map[string]interface{}{
			"correlation.id": cid,
		},
	}

	payloadJSON, _ := json.Marshal(payloadData)
	return string(payloadJSON)
}

// buildErrorPayload creates a structured payload for error logging
func (m *LoggingMiddleware) buildErrorPayload(providerName, model, endpoint, userInput string, err error, cid string, latencyMs int64) string {
	payloadData := map[string]interface{}{
		"provider": map[string]interface{}{
			"gateway.provider.name":       providerName,
			"gateway.provider.model":      model,
			"gateway.provider.endpoint":   endpoint,
			"gateway.provider.latency_ms": latencyMs,
		},
		"request": map[string]interface{}{
			"user_input": userInput,
		},
		"error": map[string]interface{}{
			"message": err.Error(),
			"type":    "provider_error",
		},
		"correlation": map[string]interface{}{
			"correlation.id": cid,
		},
	}

	payloadJSON, _ := json.Marshal(payloadData)
	return string(payloadJSON)
}

// buildResponsePayload creates a structured payload for response logging
func (m *LoggingMiddleware) buildResponsePayload(providerName string, metadata ProviderMetadata, cid string, latencyMs int64) string {
	payloadData := map[string]interface{}{
		"provider": map[string]interface{}{
			"gateway.provider.name":        providerName,
			"gateway.provider.response_id": metadata.ResponseID,
			"gateway.provider.latency_ms":  latencyMs,
		},
		"response": map[string]interface{}{
			"response_text": metadata.ResponseText,
			"finish_reason": metadata.FinishReason,
			"model":         metadata.Model,
		},
		"usage": map[string]interface{}{
			"input_tokens":  metadata.PromptTokens,
			"output_tokens": metadata.CompletionTokens,
			"total_tokens":  metadata.TotalTokens,
		},
		"correlation": map[string]interface{}{
			"correlation.id": cid,
		},
	}

	payloadJSON, _ := json.Marshal(payloadData)
	return string(payloadJSON)
}

// buildStreamResponsePayload creates a structured payload for stream completion logging
func (m *LoggingMiddleware) buildStreamResponsePayload(providerName string, metadata StreamMetadata, cid string, latencyMs int64, responseText string) string {
	payloadData := map[string]interface{}{
		"provider": map[string]interface{}{
			"gateway.provider.name":        providerName,
			"gateway.provider.response_id": metadata.ResponseID,
			"gateway.provider.latency_ms":  latencyMs,
		},
		"response": map[string]interface{}{
			"response_text":    responseText,
			"chatbot_output":   responseText, // Alias for compatibility
			"first_chunk_text": metadata.FirstChunkText,
			"finish_reason":    metadata.FinishReason,
			"model":            metadata.Model,
			"chunks_received":  metadata.TotalChunks,
		},
		"usage": map[string]interface{}{
			"input_tokens":  metadata.PromptTokens,
			"output_tokens": metadata.CompletionTokens,
			"total_tokens":  metadata.TotalTokens,
		},
		"correlation": map[string]interface{}{
			"correlation.id": cid,
		},
	}

	payloadJSON, _ := json.Marshal(payloadData)
	return string(payloadJSON)
}

// buildStreamResponsePayloadWithMetrics creates a structured payload with streaming performance metrics
func (m *LoggingMiddleware) buildStreamResponsePayloadWithMetrics(providerName string, metadata StreamMetadata, cid string, latencyMs int64, responseText string, streamMetrics streamingMetricsData) string {
	payloadData := map[string]interface{}{
		"provider": map[string]interface{}{
			"gateway.provider.name":        providerName,
			"gateway.provider.response_id": metadata.ResponseID,
			"gateway.provider.latency_ms":  latencyMs,
		},
		"response": map[string]interface{}{
			"response_text":    responseText,
			"chatbot_output":   responseText, // Alias for compatibility
			"first_chunk_text": metadata.FirstChunkText,
			"finish_reason":    metadata.FinishReason,
			"model":            metadata.Model,
			"chunks_received":  metadata.TotalChunks,
		},
		"usage": map[string]interface{}{
			"input_tokens":  metadata.PromptTokens,
			"output_tokens": metadata.CompletionTokens,
			"total_tokens":  metadata.TotalTokens,
		},
		"correlation": map[string]interface{}{
			"correlation.id": cid,
		},
		"streaming_metrics": map[string]interface{}{
			"ttft_ms":               streamMetrics.TtftMs,
			"chunk_count":           streamMetrics.ChunkCount,
			"avg_chunk_latency_ms":  streamMetrics.AvgChunkLatencyMs,
			"max_chunk_latency_ms":  streamMetrics.MaxChunkLatencyMs,
			"tokens_per_second":     streamMetrics.TokensPerSecond,
			"stream_duration_ms":    streamMetrics.StreamDurationMs,
			"chunk_timeline":        streamMetrics.ChunkTimeline,
		},
	}

	payloadJSON, _ := json.Marshal(payloadData)
	return string(payloadJSON)
}

// buildStreamErrorPayload creates a structured payload for stream error logging with streaming metrics
func (m *LoggingMiddleware) buildStreamErrorPayload(providerName, model, endpoint, userInput string, err error, cid string, latencyMs int64, streamMetrics streamingMetricsData) string {
	payloadData := map[string]interface{}{
		"provider": map[string]interface{}{
			"gateway.provider.name":       providerName,
			"gateway.provider.model":      model,
			"gateway.provider.endpoint":   endpoint,
			"gateway.provider.latency_ms": latencyMs,
		},
		"request": map[string]interface{}{
			"user_input": userInput,
		},
		"error": map[string]interface{}{
			"message": err.Error(),
			"type":    "provider_error",
		},
		"correlation": map[string]interface{}{
			"correlation.id": cid,
		},
		"streaming_metrics": map[string]interface{}{
			"ttft_ms":                   streamMetrics.TtftMs,
			"chunk_count":               streamMetrics.ChunkCount,
			"avg_chunk_latency_ms":      streamMetrics.AvgChunkLatencyMs,
			"max_chunk_latency_ms":      streamMetrics.MaxChunkLatencyMs,
			"tokens_per_second":         streamMetrics.TokensPerSecond,
			"stream_duration_ms":        streamMetrics.StreamDurationMs,
			"partial_response_on_error": streamMetrics.PartialResponseOnError,
			"chunk_timeline":            streamMetrics.ChunkTimeline,
		},
	}

	payloadJSON, _ := json.Marshal(payloadData)
	return string(payloadJSON)
}

// buildEmbeddingRequestPayload creates a structured payload for embedding request logging
func (m *LoggingMiddleware) buildEmbeddingRequestPayload(providerName, model, inputText, cid string) string {
	payloadData := map[string]interface{}{
		"provider": map[string]interface{}{
			"gateway.provider.name":  providerName,
			"gateway.provider.model": model,
		},
		"request": map[string]interface{}{
			"user_input":           inputText,
			"gateway.request.type": "embedding",
			"model":                model,
		},
		"correlation": map[string]interface{}{
			"correlation.id": cid,
		},
	}

	payloadJSON, _ := json.Marshal(payloadData)
	return string(payloadJSON)
}

// buildEmbeddingResponsePayload creates a structured payload for embedding response logging
func (m *LoggingMiddleware) buildEmbeddingResponsePayload(providerName, model string, summary embeddingSummary, promptTokens int, cid string, latencyMs int64) string {
	// Create a human-readable output description instead of raw float array
	outputDescription := formatEmbeddingOutput(summary)

	payloadData := map[string]interface{}{
		"provider": map[string]interface{}{
			"gateway.provider.name":       providerName,
			"gateway.provider.model":      model,
			"gateway.provider.latency_ms": latencyMs,
		},
		"response": map[string]interface{}{
			// Human-readable output for display
			"response_text":    outputDescription,
			"embedding_output": outputDescription,
			// Embedding metadata
			"dimension":     summary.Dimension,
			"magnitude":     fmt.Sprintf("%.4f", summary.Magnitude),
			"is_normalized": summary.IsNormalized,
			"hash":          summary.Hash,
			// Statistics
			"min_value":  summary.MinValue,
			"max_value":  summary.MaxValue,
			"mean_value": summary.MeanValue,
		},
		"usage": map[string]interface{}{
			"input_tokens": promptTokens,
			"total_tokens": promptTokens,
		},
		"correlation": map[string]interface{}{
			"correlation.id": cid,
		},
	}

	payloadJSON, _ := json.Marshal(payloadData)
	return string(payloadJSON)
}
