package tracing

import (
	"context"
	"sort"
	"strings"
	"time"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/modelidentity"
	"github.com/everstacklabs/everstack/internal/providers/health"
	"github.com/everstacklabs/everstack/internal/services/catalog"
	"github.com/everstacklabs/everstack/internal/telemetry"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"github.com/everstacklabs/everstack/internal/telemetry/metrics"
	"github.com/everstacklabs/everstack/internal/telemetry/tokens"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Global token estimator for per-message token estimation
var globalTokenEstimator = tokens.NewEstimator()

// TracingMiddleware wraps a provider with distributed tracing
type TracingMiddleware struct {
	provider          gw.Provider
	catalogCache      *catalog.Cache
	costCalculator    *metrics.CostCalculator
	selectedKeyID     string
	selectedKeyName   string
	selectedKeySource string
}

// SetCostCalculator sets the cost calculator for the middleware
func (m *TracingMiddleware) SetCostCalculator(calc *metrics.CostCalculator) {
	m.costCalculator = calc
}

// NewMiddleware creates a new tracing middleware wrapper
func NewMiddleware(provider gw.Provider) gw.Provider {
	return &TracingMiddleware{
		provider: provider,
	}
}

// NewMiddlewareWithCatalog creates middleware with cost calculation support
func NewMiddlewareWithCatalog(provider gw.Provider, catalogCache *catalog.Cache) gw.Provider {
	return &TracingMiddleware{
		provider:       provider,
		catalogCache:   catalogCache,
		costCalculator: metrics.NewCostCalculatorFromCache(catalogCache),
	}
}

// NewMiddlewareWithKey creates tracing middleware that also stamps the
// upstream provider API key that served the call. catalogCache may be nil
// (no cost calculation). keyID/keyName/keySource may be empty (legacy single-key path).
func NewMiddlewareWithKey(provider gw.Provider, catalogCache *catalog.Cache, keyID, keyName, keySource string) gw.Provider {
	m := &TracingMiddleware{
		provider:          provider,
		catalogCache:      catalogCache,
		selectedKeyID:     keyID,
		selectedKeyName:   keyName,
		selectedKeySource: keySource,
	}
	if catalogCache != nil {
		m.costCalculator = metrics.NewCostCalculatorFromCache(catalogCache)
	}
	return m
}

// Name returns the wrapped provider's name
func (m *TracingMiddleware) Name() string {
	return m.provider.Name()
}

// Unwrap returns the inner provider, allowing capability discovery through middleware layers.
func (m *TracingMiddleware) Unwrap() gw.Provider {
	return m.provider
}

// SupportsModel delegates to the wrapped provider
func (m *TracingMiddleware) SupportsModel(model string) bool {
	return m.provider.SupportsModel(model)
}

// Chat wraps the provider's Chat method with tracing
func (m *TracingMiddleware) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	// Check if provider tracing is enabled
	config := telemetry.GetGlobalTracingConfig()
	if config == nil || !config.TraceProviderCalls {
		// Tracing disabled, call provider directly
		resp, err := m.provider.Chat(ctx, req)
		if err == nil && m.selectedKeySource != "" {
			resp.KeySource = m.selectedKeySource
		}
		return resp, err
	}

	// Start child span for provider call
	ctx, span := telemetry.StartProviderSpan(ctx, m.provider.Name(), req.Model)
	defer span.End()

	// Propagate tenant.id to provider span for metrics aggregation
	if tenantID := contextkeys.ExtractTenantID(ctx); tenantID != "" {
		span.SetAttributes(attribute.String(attrs.TenantID, tenantID))
	}
	m.setModelMetricsIdentity(span, ctx, req.Model)
	if m.selectedKeyID != "" && !contextkeys.IsInternalCall(ctx) {
		span.SetAttributes(
			attribute.String(attrs.ProviderAPIKeyID, m.selectedKeyID),
			attribute.String(attrs.ProviderAPIKeyName, m.selectedKeyName),
			attribute.String(attrs.ProviderAPIKeySource, m.selectedKeySource),
		)
	}

	// Add step number from trace context
	traceCtx := telemetry.GetTraceContext(ctx)
	if traceCtx != nil {
		stepNum := traceCtx.NextStep()
		telemetry.SetStepNumber(span, stepNum)
	}

	// Add node name (provider name for now, will be workflow node later)
	telemetry.SetNodeName(span, m.provider.Name())

	// Use centralized setters for request attributes
	msgMetadata := attrs.AnalyzeMessages(req.Messages)
	attrs.SetLLMRequest(span, req.Model, msgMetadata, req.Sampling)
	attrs.SetLLMRequestPayload(span, req.Messages)

	// Estimate request body size for HTTP details
	requestBodySize := len(attrs.SerializeToJSON(req))

	// Add provider call start event
	telemetry.AddSpanEvent(span, attrs.EventProviderCallStart,
		attribute.String("provider", m.provider.Name()),
		attribute.String("model", req.Model),
		attribute.Bool("stream", false))

	// Call actual provider
	start := time.Now()
	resp, err := m.provider.Chat(ctx, req)
	latencyMs := time.Since(start).Milliseconds()

	// Record health metrics (skip for internal calls like semantic cache embeddings)
	if !contextkeys.IsInternalCall(ctx) {
		health.RecordRequest(m.provider.Name(), latencyMs, err)
	}

	// Record response metrics
	if err != nil {
		// Add provider call complete event with error
		telemetry.AddSpanEvent(span, attrs.EventProviderCallComplete,
			attribute.Int64("latency_ms", latencyMs),
			attribute.Bool("success", false),
			attribute.String("error", err.Error()))

		telemetry.RecordError(span, err)
		telemetry.SetObservationLevel(span, telemetry.ObservationLevelError)
		return gw.ChatCompletionResponse{}, err
	}
	if m.selectedKeySource != "" {
		resp.KeySource = m.selectedKeySource
	}

	// Use centralized setters for response attributes
	respMetadata := attrs.AnalyzeResponse(resp)
	attrs.SetLLMResponse(span, resp.Model, resp.ID, respMetadata)
	attrs.SetLLMResponsePayload(span, resp.Choices)
	if resp.Model != "" {
		m.setModelMetricsIdentity(span, ctx, resp.Model)
	}

	// Estimate response body size
	responseBodySize := len(attrs.SerializeToJSON(resp))

	// Set HTTP-like details (these are internal calls but we record size/timing)
	attrs.SetHTTPRequest(span, "POST", "/v1/chat/completions", requestBodySize)
	attrs.SetHTTPResponse(span, 200, responseBodySize, latencyMs)

	// Add provider call complete event
	telemetry.AddSpanEvent(span, attrs.EventProviderCallComplete,
		attribute.Int64("latency_ms", latencyMs),
		attribute.Bool("success", true),
		attribute.Int("response_tokens", resp.Usage.TotalTokens))

	// Record token usage and costs
	if resp.Usage.PromptTokens > 0 || resp.Usage.CompletionTokens > 0 {
		attrs.SetLLMTokens(span,
			int64(resp.Usage.PromptTokens),
			int64(resp.Usage.CompletionTokens),
			int64(resp.Usage.TotalTokens),
		)

		// Record detailed token breakdown if available. This shares
		// tokenBreakdowns with the streaming path deliberately: the copy
		// that used to live here had drifted and dropped
		// CacheRead/CacheWrite from the completion details.
		promptBreakdown, completionBreakdown := tokenBreakdowns(&resp.Usage)

		// Estimate per-message token counts when detailed granularity is enabled
		var perMessageTokens []int
		if config.Granularity == "detailed" && len(req.Messages) > 0 {
			perMessageTokens = globalTokenEstimator.GetPerMessageTokenCounts(req.Messages)
		}

		if promptBreakdown != nil || completionBreakdown != nil || len(perMessageTokens) > 0 {
			attrs.SetLLMTokenBreakdown(span, promptBreakdown, completionBreakdown, perMessageTokens)
		}

		// Calculate costs if calculator is available
		if m.costCalculator != nil {
			breakdown := m.costCalculator.CalculateCost(
				m.provider.Name(),
				resp.Model,
				resp.Usage.PromptTokens,
				resp.Usage.CompletionTokens,
				resp.Usage.CacheReadCount(),
			)

			attrs.SetLLMCost(span, breakdown.InputCost, breakdown.OutputCost, breakdown.EstimatedUSD)

			// Also set usage_details JSON for consistency with cost_details
			usageDetailsJSON := attrs.SerializeToJSON(map[string]int64{
				"input":  int64(resp.Usage.PromptTokens),
				"output": int64(resp.Usage.CompletionTokens),
				"total":  int64(resp.Usage.TotalTokens),
			})
			if usageDetailsJSON != "" {
				span.SetAttributes(attribute.String("llm.usage_details", usageDetailsJSON))
			}

			// Calculate and record token efficiency metrics
			attrs.SetTokenEfficiency(span,
				int64(resp.Usage.TotalTokens),
				latencyMs,
				breakdown.EstimatedUSD,
			)

			// Also record latency
			span.SetAttributes(attribute.Int64("latency_ms", latencyMs))
		} else {
			// Fallback to old method without cost breakdown
			telemetry.RecordLLMMetrics(span,
				int64(resp.Usage.PromptTokens),
				int64(resp.Usage.CompletionTokens),
				0.0,
				latencyMs,
			)
		}
	}

	span.SetStatus(codes.Ok, "success")
	return resp, nil
}

// ChatStream wraps the provider's ChatStream method with tracing
func (m *TracingMiddleware) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	withKeySource := func(chunk gw.ChatResponseChunk) error {
		if chunk.Usage != nil && m.selectedKeySource != "" {
			chunk.Usage.KeySource = m.selectedKeySource
		}
		return onChunk(chunk)
	}

	// Check if provider tracing is enabled
	config := telemetry.GetGlobalTracingConfig()
	if config == nil || !config.TraceProviderCalls {
		// Tracing disabled, call provider directly
		return m.provider.ChatStream(ctx, req, withKeySource)
	}

	// Start child span for provider stream call
	ctx, span := telemetry.StartProviderSpan(ctx, m.provider.Name(), req.Model)
	defer span.End()

	// Propagate tenant.id to provider span for metrics aggregation
	if tenantID := contextkeys.ExtractTenantID(ctx); tenantID != "" {
		span.SetAttributes(attribute.String(attrs.TenantID, tenantID))
	}
	m.setModelMetricsIdentity(span, ctx, req.Model)
	if m.selectedKeyID != "" && !contextkeys.IsInternalCall(ctx) {
		span.SetAttributes(
			attribute.String(attrs.ProviderAPIKeyID, m.selectedKeyID),
			attribute.String(attrs.ProviderAPIKeyName, m.selectedKeyName),
			attribute.String(attrs.ProviderAPIKeySource, m.selectedKeySource),
		)
	}

	// Add step number and node name
	traceCtx := telemetry.GetTraceContext(ctx)
	if traceCtx != nil {
		stepNum := traceCtx.NextStep()
		telemetry.SetStepNumber(span, stepNum)
	}
	telemetry.SetNodeName(span, m.provider.Name())

	// Use centralized setters for request attributes
	msgMetadata := attrs.AnalyzeMessages(req.Messages)
	attrs.SetLLMRequest(span, req.Model, msgMetadata, req.Sampling)
	// Capture the full request payload on the streaming span too, matching the
	// non-streaming Chat path; without this, streaming provider spans (the common
	// case) carried metadata only and rendered blank input in the trace UI.
	attrs.SetLLMRequestPayload(span, req.Messages)
	span.SetAttributes(attribute.Bool(attrs.LLMRequestStream, true))

	// Add provider call start event
	telemetry.AddSpanEvent(span, attrs.EventProviderCallStart,
		attribute.String("provider", m.provider.Name()),
		attribute.String("model", req.Model),
		attribute.Bool("stream", true))

	// Track streaming metrics
	start := time.Now()
	chunkCount := 0
	totalContentLength := 0
	totalTokensStreamed := 0
	var firstChunkTime time.Time
	var lastChunkTime time.Time
	var chunkLatencies []int64
	var streamUsage *gw.Usage // Capture usage from final chunk
	var streamModel string
	// Accumulate streamed assistant content per choice index so the full response
	// payload can be attached to the span after the stream completes.
	assembled := map[int]*strings.Builder{}
	finishReasons := map[int]string{}

	// Wrap callback to track chunks
	wrappedCallback := func(chunk gw.ChatResponseChunk) error {
		now := time.Now()
		if chunkCount == 0 {
			firstChunkTime = now
			// Add stream first chunk event
			telemetry.AddSpanEvent(span, attrs.EventStreamFirstChunk,
				attribute.Int64("latency_ms", now.Sub(start).Milliseconds()))
		} else {
			// Track inter-chunk latency
			chunkLatencies = append(chunkLatencies, now.Sub(lastChunkTime).Milliseconds())
		}
		lastChunkTime = now
		chunkCount++

		// Capture model from chunk (providers send it in early chunks)
		if chunk.Model != "" {
			streamModel = chunk.Model
		}

		// Track content length and accumulate the assistant text per choice so
		// the full response payload can be reconstructed after the stream.
		for _, choice := range chunk.Choices {
			b := assembled[choice.Index]
			if b == nil {
				b = &strings.Builder{}
				assembled[choice.Index] = b
			}
			for _, part := range choice.Delta.Content {
				if part.Text != nil {
					totalContentLength += len(*part.Text)
					b.WriteString(*part.Text)
				}
			}
			if choice.FinishReason != "" {
				finishReasons[choice.Index] = choice.FinishReason
			}
		}

		// Capture usage from final chunk (most providers send usage in the last chunk)
		if chunk.Usage != nil {
			streamUsage = chunk.Usage
		}

		// Estimate tokens (rough: 1 token ~ 4 chars)
		if totalContentLength > 0 {
			totalTokensStreamed = totalContentLength / 4
		}

		// Optionally trace individual chunks if detailed mode
		if config.TraceStreamChunks {
			_, chunkSpan := telemetry.StartStreamChunkSpan(ctx, chunkCount-1)
			chunkSpan.SetAttributes(
				attribute.Int("llm.stream.chunk_index", chunkCount-1),
			)
			chunkSpan.End()
		}

		return withKeySource(chunk)
	}

	// Add stream start event
	telemetry.AddSpanEvent(span, attrs.EventStreamStart,
		attribute.String("model", req.Model))

	// Call actual provider stream
	err := m.provider.ChatStream(ctx, req, wrappedCallback)
	totalLatencyMs := time.Since(start).Milliseconds()

	// Calculate average chunk latency
	var avgChunkLatencyMs float64
	if len(chunkLatencies) > 0 {
		var sum int64
		for _, l := range chunkLatencies {
			sum += l
		}
		avgChunkLatencyMs = float64(sum) / float64(len(chunkLatencies))
	}

	// Record streaming metrics using centralized setter
	if chunkCount > 0 {
		firstChunkLatencyMs := firstChunkTime.Sub(start).Milliseconds()
		attrs.SetLLMStreamMetrics(span, chunkCount, firstChunkLatencyMs, totalLatencyMs, totalContentLength)
		// Also set extended streaming attributes
		attrs.SetStreamingMetrics(span, firstChunkLatencyMs, totalLatencyMs, chunkCount, totalTokensStreamed, totalContentLength, avgChunkLatencyMs)
	}

	// Record token usage and costs from the stream's accumulated usage.
	// Most providers (OpenAI, Anthropic, etc.) send usage data in the final chunk.
	if streamUsage != nil && (streamUsage.PromptTokens > 0 || streamUsage.CompletionTokens > 0) {
		attrs.SetLLMTokens(span,
			int64(streamUsage.PromptTokens),
			int64(streamUsage.CompletionTokens),
			int64(streamUsage.TotalTokens),
		)
		promptBreakdown, completionBreakdown := tokenBreakdowns(streamUsage)
		if promptBreakdown != nil || completionBreakdown != nil {
			attrs.SetLLMTokenBreakdown(span, promptBreakdown, completionBreakdown, nil)
		}

		// Calculate costs if calculator is available
		if m.costCalculator != nil {
			model := streamModel
			if model == "" {
				model = req.Model
			}
			breakdown := m.costCalculator.CalculateCost(
				m.provider.Name(),
				model,
				streamUsage.PromptTokens,
				streamUsage.CompletionTokens,
				streamUsage.CacheReadCount(),
			)
			attrs.SetLLMCost(span, breakdown.InputCost, breakdown.OutputCost, breakdown.EstimatedUSD)
		}
	}
	if streamModel != "" {
		m.setModelMetricsIdentity(span, ctx, streamModel)
	}

	// Add stream complete event
	telemetry.AddSpanEvent(span, attrs.EventStreamComplete,
		attribute.Int("chunk_count", chunkCount),
		attribute.Int64("total_ms", totalLatencyMs),
		attribute.Int("tokens_streamed", totalTokensStreamed))

	// Add provider call complete event
	telemetry.AddSpanEvent(span, attrs.EventProviderCallComplete,
		attribute.Int64("latency_ms", totalLatencyMs),
		attribute.Bool("success", err == nil),
		attribute.Int("chunks", chunkCount))

	if err != nil {
		telemetry.RecordError(span, err)
		return err
	}

	// Attach the assembled response payload so the streaming span carries the
	// full output (llm.response.choices), matching the non-streaming path.
	if len(assembled) > 0 {
		indices := make([]int, 0, len(assembled))
		for idx := range assembled {
			indices = append(indices, idx)
		}
		sort.Ints(indices)
		choices := make([]gw.Choice, 0, len(indices))
		for _, idx := range indices {
			text := assembled[idx].String()
			choices = append(choices, gw.Choice{
				Index: idx,
				Message: gw.Message{
					Role:    gw.RoleAssistant,
					Content: []gw.ContentPart{{Type: "text", Text: &text}},
				},
				FinishReason: finishReasons[idx],
			})
		}
		attrs.SetLLMResponsePayload(span, choices)
	}

	span.SetStatus(codes.Ok, "success")
	return nil
}

// Embed wraps the provider's Embed method with tracing
func (m *TracingMiddleware) Embed(ctx context.Context, req gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	// Skip tracing entirely for internal calls (like semantic cache embedding generation)
	// These should not appear in the request journey
	if contextkeys.IsInternalCall(ctx) {
		return m.provider.Embed(ctx, req)
	}

	// Check if provider tracing is enabled
	config := telemetry.GetGlobalTracingConfig()
	if config == nil || !config.TraceProviderCalls {
		// Tracing disabled, call provider directly
		return m.provider.Embed(ctx, req)
	}

	// Start child span for provider embedding call
	ctx, span := telemetry.StartProviderSpan(ctx, m.provider.Name(), req.Model)
	defer span.End()

	if tenantID := contextkeys.ExtractTenantID(ctx); tenantID != "" {
		span.SetAttributes(attribute.String(attrs.TenantID, tenantID))
	}
	m.setModelMetricsIdentity(span, ctx, req.Model)
	if m.selectedKeyID != "" {
		span.SetAttributes(
			attribute.String(attrs.ProviderAPIKeyID, m.selectedKeyID),
			attribute.String(attrs.ProviderAPIKeyName, m.selectedKeyName),
			attribute.String(attrs.ProviderAPIKeySource, m.selectedKeySource),
		)
	}

	// Add step number and node name
	traceCtx := telemetry.GetTraceContext(ctx)
	if traceCtx != nil {
		stepNum := traceCtx.NextStep()
		telemetry.SetStepNumber(span, stepNum)
	}
	telemetry.SetNodeName(span, m.provider.Name())

	// Add request attributes
	span.SetAttributes(
		attribute.String("llm.operation", "embeddings"),
		attribute.String("llm.request.model", req.Model),
		attribute.Int("llm.embeddings.input_length", len(req.Input)),
	)

	// Add provider call start event
	telemetry.AddSpanEvent(span, attrs.EventProviderCallStart,
		attribute.String("provider", m.provider.Name()),
		attribute.String("model", req.Model),
		attribute.String("operation", "embeddings"))

	// Call actual provider
	start := time.Now()
	resp, err := m.provider.Embed(ctx, req)
	latencyMs := time.Since(start).Milliseconds()

	// Record health metrics (skip for internal calls like semantic cache embeddings)
	if !contextkeys.IsInternalCall(ctx) {
		health.RecordRequest(m.provider.Name(), latencyMs, err)
	}

	if err != nil {
		// Add provider call complete event with error
		telemetry.AddSpanEvent(span, attrs.EventProviderCallComplete,
			attribute.Int64("latency_ms", latencyMs),
			attribute.Bool("success", false),
			attribute.String("error", err.Error()))

		telemetry.RecordError(span, err)
		return gw.EmbeddingsResponse{}, err
	}

	// Estimate tokens for embeddings (approximate: ~4 chars per token)
	estimatedTokens := len(req.Input) / 4
	if estimatedTokens < 1 {
		estimatedTokens = 1
	}

	// Set usage in response if not already set
	if resp.Usage == nil {
		resp.Usage = &gw.Usage{
			PromptTokens: estimatedTokens,
			TotalTokens:  estimatedTokens,
		}
	}

	// Calculate and record costs if calculator available
	if m.costCalculator != nil {
		breakdown := m.costCalculator.CalculateCost(
			m.provider.Name(),
			req.Model,
			resp.Usage.PromptTokens,
			0, // No output tokens for embeddings
			0, // Embeddings are not served from a prompt cache
		)
		attrs.SetLLMCost(span, breakdown.InputCost, 0, breakdown.EstimatedUSD)
	}

	// Record token usage
	attrs.SetLLMTokens(span,
		int64(resp.Usage.PromptTokens),
		0,
		int64(resp.Usage.TotalTokens),
	)

	// Add provider call complete event
	telemetry.AddSpanEvent(span, attrs.EventProviderCallComplete,
		attribute.Int64("latency_ms", latencyMs),
		attribute.Bool("success", true),
		attribute.Int("input_tokens", resp.Usage.PromptTokens))

	// Record success metrics
	span.SetAttributes(
		attribute.Int("llm.embeddings.dimension", len(resp.Embedding)),
		attribute.Int64("latency_ms", latencyMs),
	)

	span.SetStatus(codes.Ok, "success")
	return resp, nil
}

func (m *TracingMiddleware) setModelMetricsIdentity(span trace.Span, ctx context.Context, model string) {
	trafficKind := "customer"
	if contextkeys.IsInternalCall(ctx) {
		trafficKind = "internal"
	}

	var publisher, canonicalModelID string
	if m.catalogCache != nil {
		if definition, ok := m.catalogCache.GetModel(m.provider.Name(), model); ok {
			publisher = definition.Publisher
			canonicalModelID = definition.CanonicalModelID
		} else if definition, ok := m.catalogCache.GetModelByPrefix(m.provider.Name(), model); ok {
			publisher = definition.Publisher
			canonicalModelID = definition.CanonicalModelID
		}
	}
	identity := modelidentity.ResolveWithOverrides(
		m.provider.Name(),
		model,
		publisher,
		canonicalModelID,
	)
	span.SetAttributes(
		attribute.String(attrs.TrafficKind, trafficKind),
		attribute.String(attrs.ModelPublisher, identity.Publisher),
		attribute.String(attrs.CanonicalModelID, identity.CanonicalModelID),
	)
}

func tokenBreakdowns(usage *gw.Usage) (*attrs.TokenBreakdown, *attrs.TokenBreakdown) {
	if usage == nil {
		return nil, nil
	}
	var prompt, completion *attrs.TokenBreakdown
	if usage.PromptDetails != nil {
		prompt = &attrs.TokenBreakdown{
			CachedTokens:     int64(usage.PromptDetails.CachedTokens),
			CacheReadTokens:  int64(usage.PromptDetails.CacheReadTokens),
			CacheWriteTokens: int64(usage.PromptDetails.CacheWriteTokens),
			ReasoningTokens:  int64(usage.PromptDetails.ReasoningTokens),
			AudioTokens:      int64(usage.PromptDetails.AudioTokens),
			ImageTokens:      int64(usage.PromptDetails.ImageTokens),
			TextTokens:       int64(usage.PromptDetails.TextTokens),
		}
	}
	if usage.CompletionDetails != nil {
		completion = &attrs.TokenBreakdown{
			CachedTokens:     int64(usage.CompletionDetails.CachedTokens),
			CacheReadTokens:  int64(usage.CompletionDetails.CacheReadTokens),
			CacheWriteTokens: int64(usage.CompletionDetails.CacheWriteTokens),
			ReasoningTokens:  int64(usage.CompletionDetails.ReasoningTokens),
			AudioTokens:      int64(usage.CompletionDetails.AudioTokens),
			ImageTokens:      int64(usage.CompletionDetails.ImageTokens),
			TextTokens:       int64(usage.CompletionDetails.TextTokens),
		}
	}
	return prompt, completion
}
