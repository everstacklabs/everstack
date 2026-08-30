package attributes

import (
	"fmt"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/providers/ratelimit"
	"github.com/everstacklabs/everstack/internal/redact"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// RedactPII, when true, strips PII (SSN / email / credit-card) from trace input
// and output payloads before they are set on a span (D7 redaction-before-storage,
// ties to the non-negotiable no-tenant-data-leak rule). Default off so existing
// behavior is unchanged; telemetry init enables it from config when opted in.
var RedactPII = false

func maybeRedact(s string) string {
	if !RedactPII || s == "" {
		return s
	}
	return redact.Redact(s).Text
}

// SetRequestMetadata sets basic request metadata attributes on a span
func SetRequestMetadata(span trace.Span, messageCount int, stream bool, sampling gw.SamplingParams) {
	attrs := []attribute.KeyValue{
		attribute.Int(RequestMessageCount, messageCount),
		attribute.Bool(RequestStream, stream),
		attribute.Float64(RequestTemperature, sampling.Temperature),
		attribute.Float64(RequestTopP, sampling.TopP),
		attribute.Int(RequestMaxTokens, sampling.MaxTokens),
	}

	if sampling.FrequencyPenalty != 0 {
		attrs = append(attrs, attribute.Float64(RequestFrequencyPenalty, sampling.FrequencyPenalty))
	}
	if sampling.PresencePenalty != 0 {
		attrs = append(attrs, attribute.Float64(RequestPresencePenalty, sampling.PresencePenalty))
	}
	if len(sampling.Stop) > 0 {
		attrs = append(attrs, attribute.String(RequestStop, SerializeToJSON(sampling.Stop)))
	}

	span.SetAttributes(attrs...)
}

// SetMessageStructure sets message structure metadata attributes on a span
func SetMessageStructure(span trace.Span, meta MessageMetadata) {
	attrs := []attribute.KeyValue{
		attribute.String(RequestMessageRoles, SerializeRoles(meta.Roles)),
		attribute.String(RequestMessageSizes, SerializeSizes(meta.Sizes)),
		attribute.Bool(RequestHasSystemPrompt, meta.HasSystemPrompt),
		attribute.Bool(RequestHasImages, meta.HasImages),
		attribute.Bool(RequestHasToolCalls, meta.HasToolCalls),
		attribute.String(RequestContentTypes, SerializeContentTypes(meta.ContentTypes)),
		attribute.Int(RequestTotalContentChars, meta.TotalChars),
	}

	if meta.SystemPromptLength > 0 {
		attrs = append(attrs, attribute.Int(LLMRequestSystemPromptLength, meta.SystemPromptLength))
	}
	if meta.UserMessageCount > 0 {
		attrs = append(attrs, attribute.Int(LLMRequestUserMessagesCount, meta.UserMessageCount))
	}
	if meta.AssistantMessageCount > 0 {
		attrs = append(attrs, attribute.Int(LLMRequestAssistantMessagesCount, meta.AssistantMessageCount))
	}

	span.SetAttributes(attrs...)
}

// SetBusinessContext sets business context attributes (user, tenant, API key) on a span
func SetBusinessContext(span trace.Span, userID, apiKeyHash, tenantID string) {
	attrs := []attribute.KeyValue{}

	if userID != "" {
		attrs = append(attrs, attribute.String(RequestUserID, userID))
	}
	if apiKeyHash != "" {
		attrs = append(attrs, attribute.String(RequestAPIKeyHash, apiKeyHash))
	}
	if tenantID != "" {
		attrs = append(attrs, attribute.String(RequestTenantID, tenantID))
	}

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// SetNormalizationMetadata sets normalization-specific metadata on a span
func SetNormalizationMetadata(span trace.Span, durationMs int64, configApplied bool) {
	span.SetAttributes(
		attribute.Int64(NormalizationDurationMs, durationMs),
		attribute.Bool(NormalizationConfigApplied, configApplied),
	)
}

// SetModelResolution sets model resolution metadata on a span
func SetModelResolution(span trace.Span, requested, provider, resolved, strategy string, durationMs int64, routerEnabled, fallbackAvailable bool) {
	attrs := []attribute.KeyValue{
		attribute.String(ModelRequested, requested),
		attribute.String(ModelResolved, resolved),
		attribute.String(ResolutionStrategy, strategy),
		attribute.Int64(ResolutionDurationMs, durationMs),
		attribute.Bool(ResolutionRouterEnabled, routerEnabled),
		attribute.Bool(ResolutionFallbackAvailable, fallbackAvailable),
	}

	if provider != "" {
		attrs = append(attrs, attribute.String(ModelProvider, provider))
	}

	span.SetAttributes(attrs...)
}

// SetHTTPRequest sets HTTP request metadata on a span
func SetHTTPRequest(span trace.Span, method, url string, bodySize int) {
	attrs := []attribute.KeyValue{
		attribute.String(HTTPMethod, method),
		attribute.String(HTTPURL, url),
	}

	if bodySize > 0 {
		attrs = append(attrs, attribute.Int(HTTPRequestBodySize, bodySize))
	}

	span.SetAttributes(attrs...)
}

// SetHTTPResponse sets HTTP response metadata on a span
func SetHTTPResponse(span trace.Span, statusCode, bodySize int, latencyMs int64) {
	attrs := []attribute.KeyValue{
		attribute.Int(HTTPStatusCode, statusCode),
		attribute.Int64(HTTPLatencyMs, latencyMs),
	}

	if bodySize > 0 {
		attrs = append(attrs, attribute.Int(HTTPResponseBodySize, bodySize))
	}

	span.SetAttributes(attrs...)
}

// SetRateLimitInfo sets rate limit information from headers on a span
func SetRateLimitInfo(span trace.Span, info *ratelimit.RateLimitInfo) {
	if info == nil {
		return
	}

	attrs := []attribute.KeyValue{}

	if info.RequestLimit > 0 {
		attrs = append(attrs, attribute.Int(RateLimitRequests, info.RequestLimit))
	}
	if info.TokenLimit > 0 {
		attrs = append(attrs, attribute.Int(RateLimitTokens, info.TokenLimit))
	}
	if info.RequestRemaining >= 0 {
		attrs = append(attrs, attribute.Int(RateLimitRemainingRequests, info.RequestRemaining))
	}
	if info.TokenRemaining >= 0 {
		attrs = append(attrs, attribute.Int(RateLimitRemainingTokens, info.TokenRemaining))
	}
	if info.RequestReset > 0 {
		attrs = append(attrs, attribute.Int(RateLimitResetRequests, info.RequestReset))
	}
	if info.TokenReset > 0 {
		attrs = append(attrs, attribute.Int(RateLimitResetTokens, info.TokenReset))
	}

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// SetLLMRequest sets LLM request metadata and payload on a span
func SetLLMRequest(span trace.Span, model string, meta MessageMetadata, sampling gw.SamplingParams) {
	attrs := []attribute.KeyValue{
		attribute.String(LLMRequestModel, model),
		attribute.Int(LLMRequestMessageCount, len(meta.Roles)),
		attribute.Int(LLMRequestTotalContentLength, meta.TotalChars),
	}

	if meta.SystemPromptLength > 0 {
		attrs = append(attrs, attribute.Int(LLMRequestSystemPromptLength, meta.SystemPromptLength))
	}
	if meta.UserMessageCount > 0 {
		attrs = append(attrs, attribute.Int(LLMRequestUserMessagesCount, meta.UserMessageCount))
	}
	if meta.AssistantMessageCount > 0 {
		attrs = append(attrs, attribute.Int(LLMRequestAssistantMessagesCount, meta.AssistantMessageCount))
	}

	// Add model parameters as JSON
	params := map[string]interface{}{
		"temperature": sampling.Temperature,
		"top_p":       sampling.TopP,
		"max_tokens":  sampling.MaxTokens,
		"stop":        sampling.Stop,
	}
	if paramsJSON := SerializeToJSON(params); paramsJSON != "" {
		attrs = append(attrs, attribute.String(LLMRequestModelParameters, paramsJSON))
	}

	span.SetAttributes(attrs...)
}

// SetLLMRequestPayload sets the full LLM request payload (messages) on a span WITHOUT truncation
// All message content, headers, and metadata are stored completely
func SetLLMRequestPayload(span trace.Span, messages []gw.Message) {
	messagesJSON := SerializeToJSON(messages)
	if messagesJSON != "" {
		span.SetAttributes(attribute.String(LLMRequestMessages, messagesJSON))
	}
}

// SetLLMRequestPayloadTruncated sets the LLM request payload with truncation (legacy)
// Use SetLLMRequestPayload for complete storage
func SetLLMRequestPayloadTruncated(span trace.Span, messages []gw.Message) {
	messagesJSON := SerializeAndTruncate(messages, SpanTypeProvider)
	if messagesJSON != "" {
		span.SetAttributes(attribute.String(LLMRequestMessages, messagesJSON))
	}
}

// SetLLMResponse sets LLM response metadata on a span
func SetLLMResponse(span trace.Span, model, responseID string, meta ResponseMetadata) {
	attrs := []attribute.KeyValue{
		attribute.String(LLMResponseModel, model),
		attribute.String(LLMResponseID, responseID),
		attribute.Int(LLMResponseChoiceCount, meta.ChoiceCount),
		attribute.Int(LLMResponseContentLength, meta.TotalContentLength),
	}

	if len(meta.FinishReasons) > 0 {
		if len(meta.FinishReasons) == 1 {
			attrs = append(attrs, attribute.String(LLMResponseFinishReason, meta.FinishReasons[0]))
		} else {
			attrs = append(attrs, attribute.String(LLMResponseFinishReason, SerializeFinishReasons(meta.FinishReasons)))
		}
	}

	span.SetAttributes(attrs...)
}

// SetLLMResponsePayload sets the full LLM response payload (choices) on a span WITHOUT truncation
// All response content, headers, and metadata are stored completely
func SetLLMResponsePayload(span trace.Span, choices []gw.Choice) {
	choicesJSON := SerializeToJSON(choices)
	if choicesJSON != "" {
		span.SetAttributes(attribute.String(LLMResponseChoices, choicesJSON))
	}
}

// SetLLMResponsePayloadTruncated sets the LLM response payload with truncation (legacy)
// Use SetLLMResponsePayload for complete storage
func SetLLMResponsePayloadTruncated(span trace.Span, choices []gw.Choice) {
	choicesJSON := SerializeAndTruncate(choices, SpanTypeProvider)
	if choicesJSON != "" {
		span.SetAttributes(attribute.String(LLMResponseChoices, choicesJSON))
	}
}

// SetLLMTokens sets token usage attributes on a span
func SetLLMTokens(span trace.Span, inputTokens, outputTokens, totalTokens int64) {
	span.SetAttributes(
		attribute.Int64(LLMTokensInput, inputTokens),
		attribute.Int64(LLMTokensOutput, outputTokens),
		attribute.Int64(LLMTokensTotal, totalTokens),
	)
}

// SetAgentTokens sets agent-scoped token usage attributes on a span.
func SetAgentTokens(span trace.Span, inputTokens, outputTokens, totalTokens int64) {
	span.SetAttributes(
		attribute.Int64(AgentTokensInput, inputTokens),
		attribute.Int64(AgentTokensOutput, outputTokens),
		attribute.Int64(AgentTokensTotal, totalTokens),
	)
}

// TokenBreakdown represents detailed token breakdown from provider responses.
//
// CachedTokens is the aggregate (CacheReadTokens + CacheWriteTokens),
// kept for back-compat with consumers reading the legacy field.
// CacheReadTokens and CacheWriteTokens are non-overlapping subsets of
// the inclusive prompt total.
type TokenBreakdown struct {
	CachedTokens     int64 `json:"cached_tokens,omitempty"`
	CacheReadTokens  int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64 `json:"cache_write_tokens,omitempty"`
	ReasoningTokens  int64 `json:"reasoning_tokens,omitempty"`
	AudioTokens      int64 `json:"audio_tokens,omitempty"`
	ImageTokens      int64 `json:"image_tokens,omitempty"`
	TextTokens       int64 `json:"text_tokens,omitempty"`
}

// SetLLMTokenBreakdown sets detailed token breakdown attributes on a span
// This includes provider-specific token details like cached tokens, reasoning tokens, etc.
func SetLLMTokenBreakdown(span trace.Span, promptDetails, completionDetails *TokenBreakdown, perMessageTokens []int) {
	attrs := []attribute.KeyValue{}

	// Set prompt token details
	if promptDetails != nil {
		if promptDetails.CachedTokens > 0 {
			attrs = append(attrs, attribute.Int64(LLMTokensCached, promptDetails.CachedTokens))
		}
		if promptDetails.CacheReadTokens > 0 {
			attrs = append(attrs, attribute.Int64(LLMTokensCacheRead, promptDetails.CacheReadTokens))
		}
		if promptDetails.CacheWriteTokens > 0 {
			attrs = append(attrs, attribute.Int64(LLMTokensCacheWrite, promptDetails.CacheWriteTokens))
		}
		// Serialize full prompt details as JSON
		promptDetailsJSON := SerializeToJSON(promptDetails)
		if promptDetailsJSON != "" && promptDetailsJSON != "{}" {
			attrs = append(attrs, attribute.String(LLMTokensPromptDetails, promptDetailsJSON))
		}
	}

	// Set completion token details
	if completionDetails != nil {
		if completionDetails.ReasoningTokens > 0 {
			attrs = append(attrs, attribute.Int64(LLMTokensReasoning, completionDetails.ReasoningTokens))
		}
		if completionDetails.AudioTokens > 0 {
			attrs = append(attrs, attribute.Int64(LLMTokensAudio, completionDetails.AudioTokens))
		}
		if completionDetails.TextTokens > 0 {
			attrs = append(attrs, attribute.Int64(LLMTokensText, completionDetails.TextTokens))
		}
		// Serialize full completion details as JSON
		completionDetailsJSON := SerializeToJSON(completionDetails)
		if completionDetailsJSON != "" && completionDetailsJSON != "{}" {
			attrs = append(attrs, attribute.String(LLMTokensCompletionDetails, completionDetailsJSON))
		}
	}

	// Set per-message token counts if available
	if len(perMessageTokens) > 0 {
		perMessageJSON := SerializeToJSON(perMessageTokens)
		if perMessageJSON != "" {
			attrs = append(attrs, attribute.String(LLMTokensPerMessage, perMessageJSON))
		}
	}

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// SetLLMCost sets cost breakdown attributes on a span
func SetLLMCost(span trace.Span, inputCost, outputCost, totalCost float64) {
	span.SetAttributes(
		attribute.Float64(LLMCostInput, inputCost),
		attribute.Float64(LLMCostOutput, outputCost),
		attribute.Float64(LLMCostTotal, totalCost),
		attribute.String(LLMCostCurrency, "USD"),
	)

	// CRITICAL: Also set cost_details JSON for trace transformer compatibility
	// The trace transformer expects this JSON field to aggregate costs across spans
	costDetailsJSON := SerializeToJSON(map[string]float64{
		"input":  inputCost,
		"output": outputCost,
		"total":  totalCost,
	})
	if costDetailsJSON != "" {
		span.SetAttributes(attribute.String("llm.cost_details", costDetailsJSON))
	}
}

// SetLLMStreamMetrics sets streaming-specific metrics on a span
func SetLLMStreamMetrics(span trace.Span, chunkCount int, firstChunkLatencyMs, totalLatencyMs int64, totalContentLength int) {
	attrs := []attribute.KeyValue{
		attribute.Int(LLMStreamChunkCount, chunkCount),
		attribute.Int64(LLMStreamFirstChunkLatencyMs, firstChunkLatencyMs),
		attribute.Int64(LLMStreamTotalLatencyMs, totalLatencyMs),
	}

	if totalContentLength > 0 {
		attrs = append(attrs, attribute.Int(LLMStreamTotalContentLength, totalContentLength))
		if chunkCount > 0 {
			avgChunkSize := totalContentLength / chunkCount
			attrs = append(attrs, attribute.Int(LLMStreamAverageChunkSize, avgChunkSize))
		}
	}

	// Record TTFT for any stream that actually produced a chunk, including a
	// sub-millisecond one. The guard used to be `firstChunkLatencyMs > 0`,
	// which silently dropped the sample when the first chunk arrived inside
	// the same millisecond as the request -- a genuine 0 ms TTFT became
	// indistinguishable from "this was not a stream", and the model-metrics
	// view counts a sample by the attribute's presence.
	if chunkCount > 0 && firstChunkLatencyMs >= 0 {
		attrs = append(attrs, attribute.Int64(LLMStreamTimeToFirstTokenMs, firstChunkLatencyMs))
	}

	span.SetAttributes(attrs...)
}

// SetResponseProcessing sets response processing metadata on a span
func SetResponseProcessing(span trace.Span, model, responseID string, meta ResponseMetadata, durationMs int64) {
	attrs := []attribute.KeyValue{
		attribute.String(ResponseModel, model),
		attribute.String(ResponseID, responseID),
		attribute.Int(ResponseChoiceCount, meta.ChoiceCount),
		attribute.Int(ResponseTotalContentLength, meta.TotalContentLength),
		attribute.Bool(ResponseHasMultipleChoices, meta.HasMultipleChoices),
		attribute.Int64(ResponseProcessingDurationMs, durationMs),
	}

	if len(meta.FinishReasons) > 1 {
		attrs = append(attrs, attribute.String(ResponseFinishReasons, SerializeFinishReasons(meta.FinishReasons)))
	}

	span.SetAttributes(attrs...)
}

// SetTraceInput sets the trace-level input payload on the root span WITHOUT truncation
// All message content, headers, and metadata are stored completely
func SetTraceInput(span trace.Span, messages []gw.Message) {
	inputJSON := maybeRedact(SerializeToJSON(messages))
	if inputJSON != "" {
		span.SetAttributes(attribute.String(TraceInput, inputJSON))
	}
}

// SetTraceInputTruncated sets the trace-level input payload with truncation (legacy)
// Use SetTraceInput for complete storage
func SetTraceInputTruncated(span trace.Span, messages []gw.Message) {
	inputJSON := maybeRedact(SerializeAndTruncate(messages, SpanTypeNormalization))
	if inputJSON != "" {
		span.SetAttributes(attribute.String(TraceInput, inputJSON))
	}
}

// SetTraceOutput sets the trace-level output payload on the root span WITHOUT truncation
// All response content, headers, and metadata are stored completely
func SetTraceOutput(span trace.Span, choices []gw.Choice) {
	outputJSON := maybeRedact(SerializeToJSON(choices))
	if outputJSON != "" {
		span.SetAttributes(attribute.String(TraceOutput, outputJSON))
	}
}

// SetTraceOutputTruncated sets the trace-level output payload with truncation (legacy)
// Use SetTraceOutput for complete storage
func SetTraceOutputTruncated(span trace.Span, choices []gw.Choice) {
	outputJSON := maybeRedact(SerializeAndTruncate(choices, SpanTypeResponse))
	if outputJSON != "" {
		span.SetAttributes(attribute.String(TraceOutput, outputJSON))
	}
}

// SetProviderMetadata sets provider-specific metadata on a span
func SetProviderMetadata(span trace.Span, apiVersion, region, endpoint string) {
	attrs := []attribute.KeyValue{}

	if apiVersion != "" {
		attrs = append(attrs, attribute.String(ProviderAPIVersion, apiVersion))
	}
	if region != "" {
		attrs = append(attrs, attribute.String(ProviderRegion, region))
	}
	if endpoint != "" {
		attrs = append(attrs, attribute.String(ProviderEndpoint, endpoint))
	}

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// SetTimingBreakdown sets timing breakdown for different processing stages
func SetTimingBreakdown(span trace.Span, normalizationMs, resolutionMs, providerCallMs, responseProcessMs int64) {
	attrs := []attribute.KeyValue{}

	if normalizationMs > 0 {
		attrs = append(attrs, attribute.Int64(TimingNormalization, normalizationMs))
	}
	if resolutionMs > 0 {
		attrs = append(attrs, attribute.Int64(TimingModelResolution, resolutionMs))
	}
	if providerCallMs > 0 {
		attrs = append(attrs, attribute.Int64(TimingProviderCall, providerCallMs))
	}
	if responseProcessMs > 0 {
		attrs = append(attrs, attribute.Int64(TimingResponseProcess, responseProcessMs))
	}

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// SetTokenEfficiency sets token efficiency metrics on a span
func SetTokenEfficiency(span trace.Span, totalTokens int64, latencyMs int64, totalCost float64) {
	if latencyMs <= 0 || totalTokens <= 0 {
		return
	}

	// Calculate tokens per second
	latencySec := float64(latencyMs) / 1000.0
	tokensPerSec := float64(totalTokens) / latencySec

	attrs := []attribute.KeyValue{
		attribute.Float64(TokensPerSecond, tokensPerSec),
	}

	// Calculate cost per token if cost is available
	if totalCost > 0 {
		costPerToken := totalCost / float64(totalTokens)
		attrs = append(attrs, attribute.Float64(CostPerToken, costPerToken))
	}

	span.SetAttributes(attrs...)
}

// SetEnhancedStreamMetrics sets enhanced streaming metrics including inter-chunk latency and chunk size distribution
func SetEnhancedStreamMetrics(span trace.Span, chunkSizes []int, chunkLatencies []int64) {
	if len(chunkSizes) == 0 {
		return
	}

	attrs := []attribute.KeyValue{}

	// Serialize chunk sizes
	chunkSizesJSON := SerializeToJSON(chunkSizes)
	if chunkSizesJSON != "" {
		attrs = append(attrs, attribute.String(StreamChunkSizes, chunkSizesJSON))
	}

	// Calculate min/max chunk sizes
	minSize, maxSize := chunkSizes[0], chunkSizes[0]
	for _, size := range chunkSizes {
		if size < minSize {
			minSize = size
		}
		if size > maxSize {
			maxSize = size
		}
	}
	attrs = append(attrs,
		attribute.Int(StreamMinChunkSize, minSize),
		attribute.Int(StreamMaxChunkSize, maxSize),
	)

	// Calculate average inter-chunk latency if available
	if len(chunkLatencies) > 1 {
		var totalInterChunkLatency int64
		for i := 1; i < len(chunkLatencies); i++ {
			totalInterChunkLatency += chunkLatencies[i] - chunkLatencies[i-1]
		}
		avgInterChunkLatency := totalInterChunkLatency / int64(len(chunkLatencies)-1)
		attrs = append(attrs, attribute.Int64(StreamInterChunkLatencyMs, avgInterChunkLatency))
	}

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// SetErrorDetails sets structured error information on a span
func SetErrorDetails(span trace.Span, errorType string, retryable bool, provider string) {
	attrs := []attribute.KeyValue{
		attribute.Bool(ErrorRetryable, retryable),
	}

	if errorType != "" {
		attrs = append(attrs, attribute.String(ErrorType, errorType))
	}
	if provider != "" {
		attrs = append(attrs, attribute.String(ErrorProvider, provider))
	}

	span.SetAttributes(attrs...)
}

// SetCacheMetadata sets cache-related metadata on a span
func SetCacheMetadata(span trace.Span, cacheHit bool, cacheKey string) {
	attrs := []attribute.KeyValue{
		attribute.Bool(CacheHit, cacheHit),
	}

	if cacheKey != "" {
		attrs = append(attrs, attribute.String(CacheKey, cacheKey))
	}

	span.SetAttributes(attrs...)
}

// SetCacheLookup sets cache lookup attributes on a span
func SetCacheLookup(span trace.Span, cacheType string, hit bool, keyHash uint64, latencyMs int64) {
	attrs := []attribute.KeyValue{
		attribute.String(CacheType, cacheType),
		attribute.String(CacheOperation, "lookup"),
		attribute.Bool(CacheHit, hit),
		attribute.Int64(CacheLatencyMs, latencyMs),
	}

	if keyHash > 0 {
		attrs = append(attrs, attribute.String(CacheKeyHash, fmt.Sprintf("%d", keyHash)))
	}

	span.SetAttributes(attrs...)
}

// SetCacheStore sets cache store attributes on a span
func SetCacheStore(span trace.Span, cacheType string, keyHash uint64, ttlSeconds int64, sizeBytes int) {
	attrs := []attribute.KeyValue{
		attribute.String(CacheType, cacheType),
		attribute.String(CacheOperation, "store"),
	}

	if keyHash > 0 {
		attrs = append(attrs, attribute.String(CacheKeyHash, fmt.Sprintf("%d", keyHash)))
	}
	if ttlSeconds > 0 {
		attrs = append(attrs, attribute.Int64(CacheTTL, ttlSeconds))
	}
	if sizeBytes > 0 {
		attrs = append(attrs, attribute.Int(CacheSize, sizeBytes))
	}

	span.SetAttributes(attrs...)
}

// SetSemanticCacheMetrics sets semantic cache-specific metrics on a span
func SetSemanticCacheMetrics(span trace.Span, similarityScore, threshold float64, embeddingLatencyMs int64, queryLength int) {
	attrs := []attribute.KeyValue{
		attribute.Float64(SemanticSimilarityScore, similarityScore),
		attribute.Float64(SemanticThreshold, threshold),
		attribute.Int(SemanticQueryLength, queryLength),
	}

	if embeddingLatencyMs > 0 {
		attrs = append(attrs, attribute.Int64(SemanticEmbeddingLatencyMs, embeddingLatencyMs))
	}

	span.SetAttributes(attrs...)
}

// SetVectorSearchMetrics sets vector search metrics on a span
func SetVectorSearchMetrics(span trace.Span, indexName string, topK, resultCount int, latencyMs int64, bestScore float64) {
	attrs := []attribute.KeyValue{
		attribute.String(VectorSearchIndexName, indexName),
		attribute.Int(VectorSearchTopK, topK),
		attribute.Int(VectorSearchResultCount, resultCount),
		attribute.Int64(VectorSearchLatencyMs, latencyMs),
	}

	if bestScore > 0 {
		attrs = append(attrs, attribute.Float64(VectorSearchBestScore, bestScore))
	}

	span.SetAttributes(attrs...)
}

// SetMinHashMetrics sets MinHash-specific metrics on a span
func SetMinHashMetrics(span trace.Span, signatureSize, bandCount, candidateCount int, jaccardSimilarity float64, shingleSize int) {
	attrs := []attribute.KeyValue{
		attribute.Int(MinHashSignatureSize, signatureSize),
		attribute.Int(MinHashBandCount, bandCount),
		attribute.Int(MinHashShingleSize, shingleSize),
	}

	if candidateCount > 0 {
		attrs = append(attrs, attribute.Int(MinHashCandidateCount, candidateCount))
	}
	if jaccardSimilarity > 0 {
		attrs = append(attrs, attribute.Float64(MinHashJaccardSimilarity, jaccardSimilarity))
	}

	span.SetAttributes(attrs...)
}

// SetONNXMetrics sets ONNX cache-specific metrics on a span
func SetONNXMetrics(span trace.Span, modelPath string, inferenceLatencyMs int64, dimensions, inputTokenCount int, tokenizerType string) {
	attrs := []attribute.KeyValue{
		attribute.String(ONNXModelPath, modelPath),
		attribute.Int64(ONNXInferenceLatencyMs, inferenceLatencyMs),
		attribute.Int(ONNXEmbeddingDimensions, dimensions),
	}

	if inputTokenCount > 0 {
		attrs = append(attrs, attribute.Int(ONNXInputTokenCount, inputTokenCount))
	}
	if tokenizerType != "" {
		attrs = append(attrs, attribute.String(ONNXTokenizerType, tokenizerType))
	}

	span.SetAttributes(attrs...)
}

// SetAuthCacheMetrics sets auth cache-specific metrics on a span
func SetAuthCacheMetrics(span trace.Span, bloomFilterSize, hashCount int, falsePositive, validated bool, keyHash uint64) {
	attrs := []attribute.KeyValue{
		attribute.Int(AuthCacheBloomFilterSize, bloomFilterSize),
		attribute.Int(AuthCacheHashCount, hashCount),
		attribute.Bool(AuthCacheFalsePositive, falsePositive),
		attribute.Bool(AuthCacheValidated, validated),
	}

	if keyHash > 0 {
		attrs = append(attrs, attribute.String(AuthCacheKeyHash, fmt.Sprintf("%d", keyHash)))
	}

	span.SetAttributes(attrs...)
}

// SetRouterCacheMetrics sets router cache-specific metrics on a span
func SetRouterCacheMetrics(span trace.Span, warmed, resolved bool, entryCount int, modelName, provider string) {
	attrs := []attribute.KeyValue{
		attribute.Bool(RouterCacheWarmed, warmed),
		attribute.Bool(RouterCacheResolved, resolved),
	}

	if entryCount > 0 {
		attrs = append(attrs, attribute.Int(RouterCacheEntryCount, entryCount))
	}
	if modelName != "" {
		attrs = append(attrs, attribute.String(RouterCacheModelName, modelName))
	}
	if provider != "" {
		attrs = append(attrs, attribute.String(RouterCacheProvider, provider))
	}

	span.SetAttributes(attrs...)
}

// SetEmbeddingMetrics sets embedding generation metrics on a span
func SetEmbeddingMetrics(span trace.Span, model string, dimensions, inputLength int, latencyMs int64) {
	attrs := []attribute.KeyValue{
		attribute.String(SemanticEmbeddingModel, model),
		attribute.Int(SemanticEmbeddingDimensions, dimensions),
		attribute.Int64(SemanticEmbeddingLatencyMs, latencyMs),
	}

	if inputLength > 0 {
		attrs = append(attrs, attribute.Int(SemanticQueryLength, inputLength))
	}

	span.SetAttributes(attrs...)
}

// SetCostDetails sets comprehensive cost tracking attributes on a span
func SetCostDetails(span trace.Span, estimatedUSD, actualUSD, savingsUSD, carbonSavedGrams float64, pricingModel string) {
	attrs := []attribute.KeyValue{
		attribute.Float64(CostEstimatedUSD, estimatedUSD),
		attribute.String(CostPricingModel, pricingModel),
	}

	if actualUSD > 0 {
		attrs = append(attrs, attribute.Float64(CostActualUSD, actualUSD))
	}
	if savingsUSD > 0 {
		attrs = append(attrs, attribute.Float64(CostSavingsUSD, savingsUSD))
	}
	if carbonSavedGrams > 0 {
		attrs = append(attrs, attribute.Float64(CarbonSavedGrams, carbonSavedGrams))
	}

	span.SetAttributes(attrs...)
}

// SetPerformanceMetrics sets detailed performance metrics on a span
func SetPerformanceMetrics(span trace.Span, ttfbMs, p50Ms, p95Ms, p99Ms, throughputTPS float64, latencyCategory string) {
	attrs := []attribute.KeyValue{}

	if ttfbMs > 0 {
		attrs = append(attrs, attribute.Float64(PerformanceTTFBMs, ttfbMs))
	}
	if p50Ms > 0 {
		attrs = append(attrs, attribute.Float64(PerformanceLatencyP50Ms, p50Ms))
	}
	if p95Ms > 0 {
		attrs = append(attrs, attribute.Float64(PerformanceLatencyP95Ms, p95Ms))
	}
	if p99Ms > 0 {
		attrs = append(attrs, attribute.Float64(PerformanceLatencyP99Ms, p99Ms))
	}
	if throughputTPS > 0 {
		attrs = append(attrs, attribute.Float64(PerformanceThroughputTokensPerSec, throughputTPS))
	}
	if latencyCategory != "" {
		attrs = append(attrs, attribute.String(PerformanceLatencyCategory, latencyCategory))
	}

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// SetBusinessMetrics sets business intelligence attributes on a span
func SetBusinessMetrics(span trace.Span, useCase, domain, queryType string, qualityScore float64) {
	attrs := []attribute.KeyValue{}

	if useCase != "" {
		attrs = append(attrs, attribute.String(BusinessUseCase, useCase))
	}
	if domain != "" {
		attrs = append(attrs, attribute.String(BusinessDomain, domain))
	}
	if queryType != "" {
		attrs = append(attrs, attribute.String(BusinessQueryType, queryType))
	}
	if qualityScore > 0 {
		attrs = append(attrs, attribute.Float64(BusinessResponseQualityScore, qualityScore))
	}

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// SetUserContext sets user and quota information on a span
func SetUserContext(span trace.Span, userID, tier, sessionID string, requestsToday, quotaRemaining int) {
	attrs := []attribute.KeyValue{}

	if userID != "" {
		attrs = append(attrs, attribute.String(UserID, userID))
	}
	if tier != "" {
		attrs = append(attrs, attribute.String(UserTier, tier))
	}
	if sessionID != "" {
		attrs = append(attrs, attribute.String(UserSessionID, sessionID))
	}
	if requestsToday > 0 {
		attrs = append(attrs, attribute.Int(UserRequestCountToday, requestsToday))
	}
	if quotaRemaining >= 0 {
		attrs = append(attrs, attribute.Int(UserQuotaRemaining, quotaRemaining))
	}

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// SetSecurityContext sets security and rate limiting attributes on a span
func SetSecurityContext(span trace.Span, apiKeyHash string, rateLimitRemaining int, rateLimitResetAt string) {
	attrs := []attribute.KeyValue{}

	if apiKeyHash != "" {
		attrs = append(attrs, attribute.String(SecurityAPIKeyHash, apiKeyHash))
	}
	if rateLimitRemaining >= 0 {
		attrs = append(attrs, attribute.Int(SecurityRateLimitRemaining, rateLimitRemaining))
	}
	if rateLimitResetAt != "" {
		attrs = append(attrs, attribute.String(SecurityRateLimitResetAt, rateLimitResetAt))
	}

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// SetCacheAdvancedMetrics sets detailed cache analytics on a span
func SetCacheAdvancedMetrics(span trace.Span, storageBackend, compression string, compressionRatio, hitRateLastHour, efficiencyPct float64, ageSeconds, retrievalMs int, cachedFromRequest string) {
	attrs := []attribute.KeyValue{}

	if storageBackend != "" {
		attrs = append(attrs, attribute.String(CacheStorageBackend, storageBackend))
	}
	if compression != "" {
		attrs = append(attrs, attribute.String(CacheCompression, compression))
	}
	if compressionRatio > 0 {
		attrs = append(attrs, attribute.Float64(CacheCompressionRatio, compressionRatio))
	}
	if hitRateLastHour > 0 {
		attrs = append(attrs, attribute.Float64(CacheHitRateLastHour, hitRateLastHour))
	}
	if efficiencyPct > 0 {
		attrs = append(attrs, attribute.Float64(CacheEfficiencyPercentage, efficiencyPct))
	}
	if ageSeconds > 0 {
		attrs = append(attrs, attribute.Int(CacheAgeSeconds, ageSeconds))
	}
	if retrievalMs > 0 {
		attrs = append(attrs, attribute.Int(CacheRetrievalMs, retrievalMs))
	}
	if cachedFromRequest != "" {
		attrs = append(attrs, attribute.String(CacheCachedFromRequest, cachedFromRequest))
	}

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// SetRequestDetails sets full request context attributes on a span
func SetRequestDetails(span trace.Span, requestID, input, clientIP, userAgent string, inputTokens, inputSizeBytes int) {
	attrs := []attribute.KeyValue{}

	if requestID != "" {
		attrs = append(attrs, attribute.String(RequestID, requestID))
	}
	if input != "" {
		attrs = append(attrs, attribute.String(RequestInput, input))
	}
	if inputTokens > 0 {
		attrs = append(attrs, attribute.Int(RequestInputTokens, inputTokens))
	}
	if inputSizeBytes > 0 {
		attrs = append(attrs, attribute.Int(RequestInputSizeBytes, inputSizeBytes))
	}
	if clientIP != "" {
		attrs = append(attrs, attribute.String(RequestClientIP, clientIP))
	}
	if userAgent != "" {
		attrs = append(attrs, attribute.String(RequestUserAgent, userAgent))
	}

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// SetResponseDetails sets full response context attributes on a span
func SetResponseDetails(span trace.Span, output, finishReason, modelUsed string, outputTokens, outputSizeBytes int) {
	attrs := []attribute.KeyValue{}

	if output != "" {
		attrs = append(attrs, attribute.String(ResponseOutput, output))
	}
	if finishReason != "" {
		attrs = append(attrs, attribute.String(ResponseFinishReason, finishReason))
	}
	if modelUsed != "" {
		attrs = append(attrs, attribute.String(ResponseModelUsed, modelUsed))
	}
	if outputTokens > 0 {
		attrs = append(attrs, attribute.Int(ResponseOutputTokens, outputTokens))
	}
	if outputSizeBytes > 0 {
		attrs = append(attrs, attribute.Int(ResponseOutputSizeBytes, outputSizeBytes))
	}

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// SetModelDetails sets comprehensive model information on a span
func SetModelDetails(span trace.Span, family, version, capabilities string, contextWindow, maxOutputTokens int) {
	attrs := []attribute.KeyValue{}

	if family != "" {
		attrs = append(attrs, attribute.String(ModelFamily, family))
	}
	if version != "" {
		attrs = append(attrs, attribute.String(ModelVersion, version))
	}
	if capabilities != "" {
		attrs = append(attrs, attribute.String(ModelCapabilities, capabilities))
	}
	if contextWindow > 0 {
		attrs = append(attrs, attribute.Int(ModelContextWindow, contextWindow))
	}
	if maxOutputTokens > 0 {
		attrs = append(attrs, attribute.Int(ModelMaxOutputTokens, maxOutputTokens))
	}

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// SetResolutionDetails sets detailed model resolution info on a span
func SetResolutionDetails(span trace.Span, input, output, fallbackModels, routerVersion, loadBalancing string, healthCheckPassed, modelAvailable bool, quotaCheck string) {
	attrs := []attribute.KeyValue{}

	if input != "" {
		attrs = append(attrs, attribute.String(ResolutionInput, input))
	}
	if output != "" {
		attrs = append(attrs, attribute.String(ResolutionOutput, output))
	}
	if fallbackModels != "" {
		attrs = append(attrs, attribute.String(ResolutionFallbackModels, fallbackModels))
	}
	if routerVersion != "" {
		attrs = append(attrs, attribute.String(ResolutionRouterVersion, routerVersion))
	}
	if loadBalancing != "" {
		attrs = append(attrs, attribute.String(ResolutionLoadBalancing, loadBalancing))
	}
	attrs = append(attrs, attribute.Bool(ResolutionHealthCheckPassed, healthCheckPassed))
	attrs = append(attrs, attribute.Bool(ResolutionModelAvailable, modelAvailable))
	if quotaCheck != "" {
		attrs = append(attrs, attribute.String(ResolutionQuotaCheck, quotaCheck))
	}

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// SetProviderDetails sets provider health and performance attributes on a span
func SetProviderDetails(span trace.Span, healthStatus string, currentLatencyMs int, errorRate float64) {
	attrs := []attribute.KeyValue{}

	if healthStatus != "" {
		attrs = append(attrs, attribute.String(ProviderHealthStatus, healthStatus))
	}
	if currentLatencyMs > 0 {
		attrs = append(attrs, attribute.Int(ProviderCurrentLatencyMs, currentLatencyMs))
	}
	if errorRate >= 0 {
		attrs = append(attrs, attribute.Float64(ProviderErrorRate, errorRate))
	}

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// SetNormalizationDetails sets detailed normalization tracking attributes on a span
func SetNormalizationDetails(span trace.Span, input, output, configVersion, rulesApplied string, transformations int, validationPassed bool, errors, warnings, schemaVersion string) {
	attrs := []attribute.KeyValue{}

	if input != "" {
		attrs = append(attrs, attribute.String(NormalizationInput, input))
	}
	if output != "" {
		attrs = append(attrs, attribute.String(NormalizationOutput, output))
	}
	if configVersion != "" {
		attrs = append(attrs, attribute.String(NormalizationConfigVersion, configVersion))
	}
	if rulesApplied != "" {
		attrs = append(attrs, attribute.String(NormalizationRulesApplied, rulesApplied))
	}
	if transformations > 0 {
		attrs = append(attrs, attribute.Int(NormalizationTransformations, transformations))
	}
	attrs = append(attrs, attribute.Bool(ValidationPassed, validationPassed))
	if errors != "" {
		attrs = append(attrs, attribute.String(ValidationErrors, errors))
	}
	if warnings != "" {
		attrs = append(attrs, attribute.String(ValidationWarnings, warnings))
	}
	if schemaVersion != "" {
		attrs = append(attrs, attribute.String(SchemaVersion, schemaVersion))
	}

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// SetStorageMetrics sets storage operation details on a span
func SetStorageMetrics(span trace.Span, operationType, protocol string, success bool, retryCount, connectionPoolSize, connectionWaitMs, bytesSent int, throughputMbps float64) {
	attrs := []attribute.KeyValue{}

	if operationType != "" {
		attrs = append(attrs, attribute.String(StorageOperationType, operationType))
	}
	if protocol != "" {
		attrs = append(attrs, attribute.String(NetworkProtocol, protocol))
	}
	attrs = append(attrs, attribute.Bool(StorageSuccess, success))
	if retryCount > 0 {
		attrs = append(attrs, attribute.Int(StorageRetryCount, retryCount))
	}
	if connectionPoolSize > 0 {
		attrs = append(attrs, attribute.Int(StorageConnectionPoolSize, connectionPoolSize))
	}
	if connectionWaitMs > 0 {
		attrs = append(attrs, attribute.Int(StorageConnectionWaitMs, connectionWaitMs))
	}
	if bytesSent > 0 {
		attrs = append(attrs, attribute.Int(NetworkBytesSent, bytesSent))
	}
	if throughputMbps > 0 {
		attrs = append(attrs, attribute.Float64(PerformanceWriteThroughputMbps, throughputMbps))
	}

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// SetStreamingMetrics sets comprehensive streaming metrics on a span
func SetStreamingMetrics(span trace.Span, firstChunkLatencyMs, totalDurationMs int64, chunkCount, tokensStreamed, bytesStreamed int, avgChunkLatencyMs float64) {
	attrs := []attribute.KeyValue{
		attribute.Int64(LLMStreamFirstChunkLatencyMs, firstChunkLatencyMs),
		attribute.Int64(LLMStreamTotalDurationMs, totalDurationMs),
		attribute.Int(LLMStreamChunkCount, chunkCount),
	}

	if tokensStreamed > 0 {
		attrs = append(attrs, attribute.Int(LLMStreamTokensStreamed, tokensStreamed))
	}
	if bytesStreamed > 0 {
		attrs = append(attrs, attribute.Int(LLMStreamBytesStreamed, bytesStreamed))
	}
	if avgChunkLatencyMs > 0 {
		attrs = append(attrs, attribute.Float64(LLMStreamAvgChunkLatencyMs, avgChunkLatencyMs))
	}

	span.SetAttributes(attrs...)
}

// SetFallbackMetrics sets fallback attempt metrics on a span
func SetFallbackMetrics(span trace.Span, attempt int, model, provider, strategy, reason string, timeoutMs, backoffMs int, success bool, latencyMs int64, errorMsg string) {
	attrs := []attribute.KeyValue{
		attribute.Int(FallbackAttempt, attempt),
		attribute.String(FallbackModel, model),
		attribute.String(FallbackStrategy, strategy),
		attribute.Bool(FallbackSuccess, success),
	}

	if provider != "" {
		attrs = append(attrs, attribute.String(FallbackProvider, provider))
	}
	if reason != "" {
		attrs = append(attrs, attribute.String(FallbackReason, reason))
	}
	if timeoutMs > 0 {
		attrs = append(attrs, attribute.Int(FallbackTimeoutMs, timeoutMs))
	}
	if backoffMs > 0 {
		attrs = append(attrs, attribute.Int(FallbackBackoffMs, backoffMs))
	}
	if latencyMs > 0 {
		attrs = append(attrs, attribute.Int64(FallbackLatencyMs, latencyMs))
	}
	if errorMsg != "" {
		attrs = append(attrs, attribute.String(FallbackError, errorMsg))
	}

	span.SetAttributes(attrs...)
}

// SetFallbackSummary sets fallback summary on the root span
func SetFallbackSummary(span trace.Span, triggered bool, attempts int, primaryModel string, exhausted bool, lastError string) {
	attrs := []attribute.KeyValue{
		attribute.Bool(FallbackTriggered, triggered),
	}

	if attempts > 0 {
		attrs = append(attrs, attribute.Int(FallbackAttempts, attempts))
	}
	if primaryModel != "" {
		attrs = append(attrs, attribute.String(FallbackPrimaryModel, primaryModel))
	}
	if exhausted {
		attrs = append(attrs, attribute.Bool(FallbackExhausted, exhausted))
	}
	if lastError != "" {
		attrs = append(attrs, attribute.String(FallbackLastError, lastError))
	}

	span.SetAttributes(attrs...)
}

// SetKeyRotationMetrics sets key rotation metrics on a span
func SetKeyRotationMetrics(span trace.Span, attempt int, reason string, success bool, keysAvailable, keysTried int, durationMs int64) {
	attrs := []attribute.KeyValue{
		attribute.Int(KeyRotationAttempt, attempt),
		attribute.Bool(KeyRotationSuccess, success),
	}

	if reason != "" {
		attrs = append(attrs, attribute.String(KeyRotationReason, reason))
	}
	if keysAvailable > 0 {
		attrs = append(attrs, attribute.Int(KeyRotationKeysAvailable, keysAvailable))
	}
	if keysTried > 0 {
		attrs = append(attrs, attribute.Int(KeyRotationKeysTried, keysTried))
	}
	if durationMs > 0 {
		attrs = append(attrs, attribute.Int64(KeyRotationDurationMs, durationMs))
	}

	span.SetAttributes(attrs...)
}

// SetCacheLookupDetails sets comprehensive cache lookup details on a span
func SetCacheLookupDetails(span trace.Span, enabled, hit bool, cacheType string, lookupDurationMs int64, exactChecked, exactHit bool, exactDurationMs int64, semanticChecked, semanticHit bool, semanticDurationMs int64, similarityScore, threshold float64, candidatesFound int) {
	attrs := []attribute.KeyValue{
		attribute.Bool(CacheEnabled, enabled),
		attribute.Bool(CacheHit, hit),
		attribute.Int64(CacheLookupDurationMs, lookupDurationMs),
	}

	if cacheType != "" {
		attrs = append(attrs, attribute.String(CacheType, cacheType))
	}

	// Exact cache details
	if exactChecked {
		attrs = append(attrs,
			attribute.Bool(CacheExactChecked, exactChecked),
			attribute.Bool(CacheExactHit, exactHit),
		)
		if exactDurationMs > 0 {
			attrs = append(attrs, attribute.Int64(CacheExactDurationMs, exactDurationMs))
		}
	}

	// Semantic cache details
	if semanticChecked {
		attrs = append(attrs,
			attribute.Bool(CacheSemanticChecked, semanticChecked),
			attribute.Bool(CacheSemanticHit, semanticHit),
		)
		if semanticDurationMs > 0 {
			attrs = append(attrs, attribute.Int64(CacheSemanticDurationMs, semanticDurationMs))
		}
		if similarityScore > 0 {
			attrs = append(attrs, attribute.Float64(SemanticSimilarityScore, similarityScore))
		}
		if threshold > 0 {
			attrs = append(attrs, attribute.Float64(SemanticThreshold, threshold))
		}
		if candidatesFound > 0 {
			attrs = append(attrs, attribute.Int(CacheCandidatesFound, candidatesFound))
		}
	}

	span.SetAttributes(attrs...)
}

// SetSemanticEmbeddingDetails sets semantic cache embedding details on a span
func SetSemanticEmbeddingDetails(span trace.Span, model string, dimensions int, latencyMs int64, inputTokens, inputTextLength int, provider string) {
	attrs := []attribute.KeyValue{
		attribute.String(LLMOperation, "embeddings"),
		attribute.String(SemanticEmbeddingModel, model),
	}

	if dimensions > 0 {
		attrs = append(attrs, attribute.Int(SemanticEmbeddingDimensions, dimensions))
	}
	if latencyMs > 0 {
		attrs = append(attrs, attribute.Int64(SemanticEmbeddingLatencyMs, latencyMs))
	}
	if inputTokens > 0 {
		attrs = append(attrs, attribute.Int(SemanticInputTokens, inputTokens))
	}
	if inputTextLength > 0 {
		attrs = append(attrs, attribute.Int(SemanticInputTextLength, inputTextLength))
	}
	if provider != "" {
		attrs = append(attrs, attribute.String(Provider, provider))
	}

	span.SetAttributes(attrs...)
}

// SetSemanticSearchDetails sets semantic cache search details on a span
func SetSemanticSearchDetails(span trace.Span, backend string, durationMs int64, candidatesReturned int, bestScore, threshold float64, thresholdMet bool) {
	attrs := []attribute.KeyValue{}

	if backend != "" {
		attrs = append(attrs, attribute.String(SemanticSearchBackend, backend))
	}
	if durationMs > 0 {
		attrs = append(attrs, attribute.Int64(SemanticSearchDurationMs, durationMs))
	}
	if candidatesReturned > 0 {
		attrs = append(attrs, attribute.Int(SemanticCandidatesReturned, candidatesReturned))
	}
	if bestScore > 0 {
		attrs = append(attrs, attribute.Float64(SemanticBestScore, bestScore))
	}
	if threshold > 0 {
		attrs = append(attrs, attribute.Float64(SemanticThreshold, threshold))
	}
	attrs = append(attrs, attribute.Bool(SemanticThresholdMet, thresholdMet))

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// SetRootSpanBusinessMetrics sets business metrics on the root span for easy querying
func SetRootSpanBusinessMetrics(span trace.Span, costInputUSD, costOutputUSD, costTotalUSD, costSavingsUSD, carbonGrams, carbonSavedGrams float64, tokensInput, tokensOutput, tokensTotal int64, latencyTotalMs, latencyTTFTMs, latencyProviderMs int64) {
	attrs := []attribute.KeyValue{}

	// Cost metrics
	if costInputUSD > 0 {
		attrs = append(attrs, attribute.Float64(CostInputUSD, costInputUSD))
	}
	if costOutputUSD > 0 {
		attrs = append(attrs, attribute.Float64(CostOutputUSD, costOutputUSD))
	}
	if costTotalUSD > 0 {
		attrs = append(attrs, attribute.Float64(CostEstimatedUSD, costTotalUSD))
	}
	if costSavingsUSD > 0 {
		attrs = append(attrs, attribute.Float64(CostSavingsUSD, costSavingsUSD))
	}

	// Carbon metrics
	if carbonGrams > 0 {
		attrs = append(attrs, attribute.Float64(CarbonGrams, carbonGrams))
	}
	if carbonSavedGrams > 0 {
		attrs = append(attrs, attribute.Float64(CarbonSavedGrams, carbonSavedGrams))
	}

	// Token metrics
	if tokensInput > 0 {
		attrs = append(attrs, attribute.Int64(TokensInput, tokensInput))
	}
	if tokensOutput > 0 {
		attrs = append(attrs, attribute.Int64(TokensOutput, tokensOutput))
	}
	if tokensTotal > 0 {
		attrs = append(attrs, attribute.Int64(TokensTotal, tokensTotal))
	}

	// Latency metrics
	if latencyTotalMs > 0 {
		attrs = append(attrs, attribute.Int64(LatencyTotalMs, latencyTotalMs))
	}
	if latencyTTFTMs > 0 {
		attrs = append(attrs, attribute.Int64(LatencyTTFTMs, latencyTTFTMs))
	}
	if latencyProviderMs > 0 {
		attrs = append(attrs, attribute.Int64(LatencyProviderMs, latencyProviderMs))
	}

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// SetTraceContext sets trace-level context attributes
func SetTraceContext(span trace.Span, environment, release, traceName string) {
	attrs := []attribute.KeyValue{}

	if environment != "" {
		attrs = append(attrs, attribute.String(TraceEnvironment, environment))
	}
	if release != "" {
		attrs = append(attrs, attribute.String(TraceRelease, release))
	}
	if traceName != "" {
		attrs = append(attrs, attribute.String(TraceName, traceName))
	}

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// SetResponseToolCallDetails sets response tool call details
func SetResponseToolCallDetails(span trace.Span, hasToolCalls, hasFunctionCall bool, outputRaw string) {
	attrs := []attribute.KeyValue{
		attribute.Bool(ResponseHasToolCalls, hasToolCalls),
		attribute.Bool(ResponseHasFunctionCall, hasFunctionCall),
	}

	if outputRaw != "" {
		attrs = append(attrs, attribute.String(ResponseOutputRaw, outputRaw))
	}

	span.SetAttributes(attrs...)
}

// SetAgentSessionMetrics sets summary metrics on an agent session span.
func SetAgentSessionMetrics(span trace.Span, turnCount, totalToolCalls, totalTokens int, finishReason string) {
	attrs := []attribute.KeyValue{
		attribute.Int(AgentTotalTurns, turnCount),
		attribute.Int(AgentTotalToolCalls, totalToolCalls),
		attribute.Int(AgentTotalTokens, totalTokens),
	}
	if finishReason != "" {
		attrs = append(attrs, attribute.String(AgentFinishReason, finishReason))
	}
	span.SetAttributes(attrs...)
}

// SetAgentSessionContext sets inferred execution/persistence context on an agent session span.
func SetAgentSessionContext(span trace.Span, executionMode, persistenceMode string, sandboxEnabled, gitRepoConfigured, templateConfigured bool) {
	attrs := []attribute.KeyValue{
		attribute.Bool(AgentSandboxEnabled, sandboxEnabled),
		attribute.Bool(AgentGitRepoConfigured, gitRepoConfigured),
		attribute.Bool(AgentTemplateConfigured, templateConfigured),
	}
	if executionMode != "" {
		attrs = append(attrs, attribute.String(AgentExecutionMode, executionMode))
	}
	if persistenceMode != "" {
		attrs = append(attrs, attribute.String(AgentPersistenceMode, persistenceMode))
	}
	span.SetAttributes(attrs...)
}

// SetAgentPolicyContext sets typed agent policy attributes on a span.
func SetAgentPolicyContext(span trace.Span, mode, taskPermissionMode, workingDirectory string, maxSteps int32) {
	attrs := []attribute.KeyValue{}
	if mode != "" {
		attrs = append(attrs, attribute.String(AgentMode, mode))
	}
	if taskPermissionMode != "" {
		attrs = append(attrs, attribute.String(AgentTaskPermissionMode, taskPermissionMode))
	}
	if maxSteps > 0 {
		attrs = append(attrs, attribute.Int(AgentMaxSteps, int(maxSteps)))
	}
	if workingDirectory != "" {
		attrs = append(attrs, attribute.String(AgentWorkingDirectory, workingDirectory))
	}
	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// SetAgentTurnMetrics sets per-turn metrics on an agent turn span.
func SetAgentTurnMetrics(span trace.Span, iteration, toolCallCount int, promptTokens, completionTokens, totalTokens int64, latencyMs int64) {
	attrs := []attribute.KeyValue{
		attribute.Int(AgentIteration, iteration),
		attribute.Int(AgentToolsCount, toolCallCount),
		attribute.Int64(LLMTokensInput, promptTokens),
		attribute.Int64(LLMTokensOutput, completionTokens),
		attribute.Int64(LLMTokensTotal, totalTokens),
		attribute.Int64(LatencyMs, latencyMs),
	}
	span.SetAttributes(attrs...)
}

// SetAgentTurnToolSummary sets per-turn tool call breakdown metrics.
func SetAgentTurnToolSummary(span trace.Span, toolCalls, sandboxToolCalls, nonSandboxToolCalls, toolErrors int) {
	span.SetAttributes(
		attribute.Int(AgentTurnToolCalls, toolCalls),
		attribute.Int(AgentTurnSandboxTools, sandboxToolCalls),
		attribute.Int(AgentTurnNonSandboxTools, nonSandboxToolCalls),
		attribute.Int(AgentTurnToolErrors, toolErrors),
	)
}

// AgentTurnSnapshot is the "moment of decision" record attached to every
// agent turn span. The verdict-rates breakdown queries (Phase 0c) use these
// fields to slice outcomes by prompt template version and context size.
// Empty fields are skipped so spans missing optional inputs aren't tagged
// with empty attribute values (which would surface as "" group keys).
type AgentTurnSnapshot struct {
	PromptTemplateID    string
	PromptVersion       string
	ReasoningTextHash   string
	ContextBytesAtStart int64
	// ToolResultSummary is a compact "ok=N err=M" string per tool name,
	// computed from the turn's emitted tool spans.
	ToolResultSummary string
}

// SetAgentTurnSnapshot writes the per-turn outcome snapshot to a turn span.
// Safe to call with a zero-value snapshot — every field is conditionally set.
func SetAgentTurnSnapshot(span trace.Span, s AgentTurnSnapshot) {
	if span == nil {
		return
	}
	kvs := make([]attribute.KeyValue, 0, 5)
	if s.PromptTemplateID != "" {
		kvs = append(kvs, attribute.String(AgentTurnPromptTemplateID, s.PromptTemplateID))
	}
	if s.PromptVersion != "" {
		kvs = append(kvs, attribute.String(AgentTurnPromptVersion, s.PromptVersion))
	}
	if s.ReasoningTextHash != "" {
		kvs = append(kvs, attribute.String(AgentTurnReasoningTextHash, s.ReasoningTextHash))
	}
	if s.ContextBytesAtStart > 0 {
		kvs = append(kvs, attribute.Int64(AgentTurnContextBytesAtStart, s.ContextBytesAtStart))
	}
	if s.ToolResultSummary != "" {
		kvs = append(kvs, attribute.String(AgentTurnToolResultSummary, s.ToolResultSummary))
	}
	if len(kvs) > 0 {
		span.SetAttributes(kvs...)
	}
}

// SetAgentToolCallResult sets result attributes on an agent tool call span.
func SetAgentToolCallResult(span trace.Span, success bool, durationMs int64, resultSize int) {
	attrs := []attribute.KeyValue{
		attribute.Bool(AgentToolCallSuccess, success),
		attribute.Int64(AgentToolCallDurationMs, durationMs),
	}
	if resultSize > 0 {
		attrs = append(attrs, attribute.Int(HTTPResponseBodySize, resultSize))
	}
	span.SetAttributes(attrs...)
}

// SetSandboxExecResult sets sandbox execution result attributes on a span.
func SetSandboxExecResult(span trace.Span, sandboxID, backend, image, language string, exitCode int, durationMs int64, timedOut bool) {
	attrs := []attribute.KeyValue{
		attribute.String(SandboxID, sandboxID),
		attribute.Int(SandboxExitCode, exitCode),
		attribute.Int64(SandboxDurationMs, durationMs),
		attribute.Bool(SandboxTimedOut, timedOut),
	}
	if backend != "" {
		attrs = append(attrs, attribute.String(SandboxBackend, backend))
	}
	if image != "" {
		attrs = append(attrs, attribute.String(SandboxImage, image))
	}
	if language != "" {
		attrs = append(attrs, attribute.String(SandboxLanguage, language))
	}
	span.SetAttributes(attrs...)
}

// SetSandboxGitCloneMetrics sets git clone metrics on a span.
func SetSandboxGitCloneMetrics(span trace.Span, durationMs int64, bytesTotal int64, strategy string, cloned bool) {
	span.SetAttributes(
		attribute.Bool(SandboxGitCloned, cloned),
		attribute.Int64(SandboxGitCloneDurationMs, durationMs),
		attribute.Int64(SandboxGitCloneBytesTotal, bytesTotal),
		attribute.String(SandboxGitCloneStrategy, strategy),
	)
}

// SetMcpToolCallMetrics sets MCP tool call attributes on a span.
func SetMcpToolCallMetrics(span trace.Span, serverID, serverName, toolName, status string, latencyMs int64) {
	span.SetAttributes(
		attribute.String(McpServerID, serverID),
		attribute.String(McpServerName, serverName),
		attribute.String(McpToolName, toolName),
		attribute.String(McpToolCallStatus, status),
		attribute.Int64(McpToolCallLatency, latencyMs),
	)
}

// SetMcpServerDiscovery sets MCP server discovery attributes on a span.
func SetMcpServerDiscovery(span trace.Span, serverID, serverName, transport string, toolCount int) {
	span.SetAttributes(
		attribute.String(McpServerID, serverID),
		attribute.String(McpServerName, serverName),
		attribute.String(McpServerTransport, transport),
		attribute.Int(McpToolCount, toolCount),
	)
}

// SetMcpHealthCheck sets MCP health check attributes on a span.
func SetMcpHealthCheck(span trace.Span, serverID, serverName, healthStatus string) {
	span.SetAttributes(
		attribute.String(McpServerID, serverID),
		attribute.String(McpServerName, serverName),
		attribute.String(McpHealthStatus, healthStatus),
	)
}
