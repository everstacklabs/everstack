package v1

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"sync/atomic"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/commands/handlers/gateway/chat"
	"github.com/everstacklabs/everstack/internal/database"
	rtconfig "github.com/everstacklabs/everstack/internal/domain/runtime_config"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/cache"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/fastpath"
	fpmetrics "github.com/everstacklabs/everstack/internal/lib/handlers/gateway/metrics"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/lib/retrypolicy"
	licensemonitor "github.com/everstacklabs/everstack/internal/services/license_monitor"
	"github.com/everstacklabs/everstack/internal/telemetry"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"github.com/everstacklabs/everstack/internal/telemetry/metrics"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/metadata"
)

// Provider latency EWMA in milliseconds (alpha≈0.2 via integer smoothing)
var providerLatencyMsEWMA int64

// updateProviderLatency updates the EWMA of provider latency
func updateProviderLatency(d time.Duration) {
	ms := d.Milliseconds()
	if ms <= 0 {
		return
	}
	old := atomic.LoadInt64(&providerLatencyMsEWMA)
	if old == 0 {
		atomic.StoreInt64(&providerLatencyMsEWMA, ms)
		return
	}
	// new = (4*old + ms)/5  (alpha = 0.2)
	newV := (4*old + ms) / 5
	atomic.StoreInt64(&providerLatencyMsEWMA, newV)
}

// ProviderLatencyMsEWMA exposes the current EWMA for tuning
func ProviderLatencyMsEWMA() int64 { return atomic.LoadInt64(&providerLatencyMsEWMA) }

// Shared handler for ChatCompletion across Connect & gRPC
func processChatCompletion(ctx context.Context, base *Server, req *gatewaypb.ChatCompletionRequest, send func(*gatewaypb.ChatCompletionResponse) error) error {
	// Per-tenant function-calling gate. Reject if the request includes
	// `tools` while the tenant has function calling disabled. The chat
	// itself still works without tools so we don't block the whole call.
	if rt := rtconfigFromCtx(ctx, base); rt != nil {
		tenantID := contextkeys.ExtractTenantID(ctx)
		if !rt.IsFunctionCallingEnabled(tenantID) && len(req.GetTools()) > 0 {
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("function calling is disabled for this tenant"))
		}
	}
	if err := base.EnsureProvidersForRequest(ctx); err != nil {
		logger.WithFields("error", err.Error()).Warn("gateway: failed to refresh tenant providers for chat request")
	}
	if base.routerFor(ctx) == nil {
		base.bootstrapFromConfig()
	}
	// Inject repository from server context into request context
	if base.ctx != nil {
		if repo := base.ctx.Value(contextkeys.ProviderRepo); repo != nil {
			ctx = context.WithValue(ctx, contextkeys.ProviderRepo, repo)
		}
	}

	// Ensure a server-side correlation ID exists for this request (with chat_ prefix)
	ctx, _ = correlation.EnsureEndpointCorrelationID(ctx, correlation.EndpointChat)

	// Extract correlation ID for consistent use across all events
	correlationID := correlation.GetCorrelationID(ctx)

	// Start distributed trace span for gateway request
	ctx, span := telemetry.StartGatewaySpan(ctx, "gateway.chat.completion",
		telemetry.WithRequestID(correlationID),
	)
	defer span.End()

	// Inference metering (shadow phase): accumulate platform-key cost as attempts
	// complete and settle once on a detached context when the request ends. nil and
	// every method is a no-op when metering does not apply (no billing DB / no org).
	meter := newInferenceMeter(ctx, base, correlationID)
	defer meter.settle(ctx)

	// Tag trace span with tenant_id for cloud instance isolation
	if tid := database.TenantSchemaFromContext(ctx); tid != "" {
		span.SetAttributes(attribute.String(attrs.TenantID, tid))
	}

	// Enrich trace context with metadata
	traceCtx := telemetry.GetTraceContext(ctx)
	traceCtx.TraceName = "Chat Completion"

	// Extract userId, sessionId from context
	if userID := base.extractUserID(ctx); userID != "" {
		traceCtx.UserID = userID
	}
	// Prefer a caller-supplied session (x-session-id header or `session_id` in
	// request metadata) so API callers can group related traces; fall back to
	// the per-request correlation id when none is provided.
	sessionID := base.extractSessionID(ctx, req.GetMetadata())
	if sessionID == "" {
		sessionID = correlationID
	}
	traceCtx.SessionID = sessionID
	// Caller-supplied conversational thread (optional), distinct from session.
	traceCtx.ThreadID = base.extractThreadID(ctx, req.GetMetadata())

	// Set environment and release from environment variables or defaults
	traceCtx.Environment = "development"
	if env := os.Getenv("DEPLOYMENT_ENV"); env != "" {
		traceCtx.Environment = env
	}
	if release := os.Getenv("APP_VERSION"); release != "" {
		traceCtx.Release = release
	}

	ctx = telemetry.WithTraceContext(ctx, traceCtx)

	startTime := time.Now()

	// Inject sticky key from gRPC metadata if configured
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		ctx = base.withKeySourceFromMetadata(ctx, md)
	}
	// pick model: request overrides; else select via LB/defaults
	model := req.GetModel()
	if model == "" {
		model = base.selectOmittedModel(ctx)
		if model == "" {
			err := connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("no default model configured - please specify a model in the request or configure a default provider"))
			span.RecordError(err)
			span.SetStatus(codes.Error, "no default model configured")
			return err
		}
	}

	// Add model to span
	span.SetAttributes(
		attribute.String("model.requested", model),
	)

	// Build normalized request with tracing
	normStart := time.Now()
	ctx, normSpan := telemetry.StartRequestNormalizationSpan(ctx)

	// Add span event for normalization start
	telemetry.AddSpanEvent(normSpan, attrs.EventNormalizationStart,
		attribute.String("input_format", "openai-compatible"))

	norm := toGatewayRequest(req)
	norm.Model = model
	if err := validateOutputTokenLimits(req.GetSampling()); err != nil {
		normSpan.End()
		return connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Apply model-scoped defaults. Pointer-backed config values preserve valid
	// zeroes such as temperature=0.
	requestSampling := norm.Sampling
	configApplied := base.requestDefaultsForModel(ctx, model).applySampling(&norm.Sampling)
	requestForModel := func(alias string) gw.ChatCompletionRequest {
		attempt := norm
		attempt.Model = alias
		attempt.Sampling = requestSampling
		base.requestDefaultsForModel(ctx, alias).applySampling(&attempt.Sampling)
		return attempt
	}
	// Presence-based defaulting: request overrides; else default to features
	// Check RuntimeConfigService first for hot-reload support, fall back to static config
	// Check both request context and server context (for grpc-gateway path)
	streamingEnabled := false
	var runtimeSvc *rtconfig.Service
	if svc, ok := ctx.Value(contextkeys.RuntimeConfigService).(*rtconfig.Service); ok && svc != nil {
		runtimeSvc = svc
	} else if base.ctx != nil {
		if svc, ok := base.ctx.Value(contextkeys.RuntimeConfigService).(*rtconfig.Service); ok && svc != nil {
			runtimeSvc = svc
		}
	}

	if runtimeSvc != nil {
		streamingEnabled = runtimeSvc.IsStreamingEnabled(contextkeys.ExtractTenantID(ctx))
		logger.Infof("processChatCompletion: streaming_enabled=%v (from RuntimeConfigService)", streamingEnabled)
	} else if base.feat != nil {
		streamingEnabled = base.feat.Gateway.EnableStreaming
		logger.Infof("processChatCompletion: streaming_enabled=%v (from static config)", streamingEnabled)
	} else {
		logger.Info("processChatCompletion: no streaming config found, using default false")
	}

	defaultStream := streamingEnabled
	// Presence-based defaulting: request overrides; else default to features
	wantStream := defaultStream
	if req.Stream != nil { // presence check (now works after proto regen)
		wantStream = req.GetStream()
	}
	// Server-side gate: if streaming requested but disabled by policy, reject
	if wantStream && !streamingEnabled {
		normSpan.End()
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("streaming is disabled by server policy"))
	}
	norm.Stream = wantStream
	if len(norm.Messages) == 0 {
		normSpan.End()
		return connect.NewError(connect.CodeInvalidArgument, errors.New("messages must contain at least 1 message"))
	}

	// Use centralized attribute setters
	// Analyze messages for rich metadata
	msgMetadata := attrs.AnalyzeMessages(norm.Messages)

	// Set request metadata
	attrs.SetRequestMetadata(normSpan, len(norm.Messages), norm.Stream, norm.Sampling)

	// Set message structure metadata
	attrs.SetMessageStructure(normSpan, msgMetadata)

	// Set business context
	userID := base.extractUserID(ctx)
	apiKeyHash := chat.HashAPIKey(base.extractAPIKey(ctx))
	tenantID := "" // TODO: Extract tenant ID if available
	attrs.SetBusinessContext(normSpan, userID, apiKeyHash, tenantID)

	// Set normalization metadata
	normDuration := time.Since(normStart).Milliseconds()
	attrs.SetNormalizationMetadata(normSpan, normDuration, configApplied)

	// Add detailed normalization attributes
	rulesApplied := []string{}
	transformations := 0
	if configApplied {
		rulesApplied = append(rulesApplied, "apply_config_defaults")
		transformations++
	}
	rulesApplied = append(rulesApplied, "strip_whitespace", "validate_schema", "add_defaults")
	transformations += 3

	attrs.SetNormalizationDetails(normSpan,
		attrs.SerializeToJSON(req.Messages),
		attrs.SerializeToJSON(norm.Messages),
		"1.2",
		strings.Join(rulesApplied, ","),
		transformations,
		true, // validation passed
		"[]", // no errors
		"[]", // no warnings
		"openai-compatible-v1")

	// Add span event for normalization complete
	telemetry.AddSpanEvent(normSpan, attrs.EventNormalizationComplete,
		attribute.Bool("output_valid", true))

	// Capture input at trace level (root span)
	attrs.SetTraceInput(span, norm.Messages)

	// Set request details on root span
	inputJSON := attrs.SerializeToJSON(norm.Messages)
	attrs.SetRequestDetails(span, correlationID, inputJSON, "", "", msgMetadata.TotalChars, len(inputJSON))

	// Update trace context with model parameters
	traceCtx = telemetry.GetTraceContext(ctx)
	traceCtx.ModelParameters = map[string]interface{}{
		"temperature":           norm.Sampling.Temperature,
		"top_p":                 norm.Sampling.TopP,
		"max_tokens":            norm.Sampling.MaxTokens,
		"max_completion_tokens": norm.Sampling.MaxCompletionTokens,
		"stop":                  norm.Sampling.Stop,
	}
	ctx = telemetry.WithTraceContext(ctx, traceCtx)

	normSpan.End()

	// Validate model exists BEFORE dispatching command with tracing
	// This prevents session.started events for model-not-found errors.
	// resolvedProvider carries the provider resolved here (in the request
	// context, with the right tenant) into the async CQRS command, which would
	// otherwise re-resolve under a tenant-less background context and blank out.
	var resolvedProvider string
	if base.routerFor(ctx) != nil {
		resolveStart := time.Now()
		ctx, resolveSpan := telemetry.StartModelResolutionSpan(ctx, model)
		provider, actualModel, err := base.routerFor(ctx).ResolveWithContext(ctx, model)
		resolveDuration := time.Since(resolveStart).Milliseconds()

		if err != nil {
			resolveSpan.RecordError(err)
			resolveSpan.SetStatus(codes.Error, "model not found")
			resolveSpan.End()

			// Model not found - emit session.error AND model.not_found events
			userID := base.extractUserID(ctx)
			apiKeyHash := chat.HashAPIKey(base.extractAPIKey(ctx))
			sessionID := correlationID // Use correlation ID as session ID for failed requests

			// Emit session.error event for complete audit trail
			base.emitSessionErrorEvent(ctx, sessionID, model, "model_not_found", err.Error(), userID, apiKeyHash, correlationID, "ChatCompletion")

			// Also emit model.not_found event for specific model tracking
			base.emitModelNotFoundEvent(ctx, model, userID, apiKeyHash, correlationID)

			return connect.NewError(connect.CodeNotFound, err)
		}

		// Prefer the resolved route's provider name. It is authoritative even
		// when the FastPath router cache hands back a provider object whose
		// Name() is generic/placeholder; fall back to the object name.
		providerName := actualModel.ProviderName
		if providerName == "" {
			providerName = provider.Name()
		}
		resolvedProvider = providerName

		// Use centralized setter for model resolution
		fallbackSteps := base.fallbackPlan(ctx)
		attrs.SetModelResolution(
			resolveSpan,
			model,                  // requested
			providerName,           // provider
			actualModel.ModelName,  // resolved
			"direct",               // strategy
			resolveDuration,        // durationMs
			true,                   // routerEnabled
			len(fallbackSteps) > 0, // fallbackAvailable
		)

		// Stamp provider + resolved model on the outer gateway.chat.completion
		// span as soon as resolution succeeds — before any provider call that
		// might time out, hang, or error. The success paths below also set
		// these (on the actual response model, which can differ from the
		// resolved model on fallback) and will overwrite. Without setting
		// here, error/timeout exits fall through to span.End() with no
		// provider attribute, and trace_metrics_hourly groups those rows
		// under provider='' — per-provider error rates undercount as a
		// result.
		span.SetAttributes(
			attribute.String("provider", providerName),
			attribute.String("model.served", actualModel.ModelName),
		)

		resolveSpan.SetStatus(codes.Ok, "model resolved")
		resolveSpan.End()
	}

	// Execute CQRS command for chat completion asynchronously (after validation, before processing)
	// This ensures session.started events only fire for valid requests that will be processed
	// Run in goroutine to avoid blocking the request path with database writes
	go func() {
		// Use background context to ensure command completes even if request is canceled
		bgCtx := correlation.WithCorrelationID(context.Background(), correlationID)
		if err := base.executeChatCommandWithModel(bgCtx, req, model, resolvedProvider, correlationID); err != nil {
			logger.WithCategory(logger.CategoryOperational).SetFields("correlation_id", correlationID, "error", err.Error()).Warn("failed to execute CQRS chat command")
		}
	}()

	// Operational logging (bridges to OTEL automatically via logger)
	// userID and apiKeyHash already extracted earlier for span attributes
	logger.WithFields(
		"correlation_id", correlationID,
		"model", model,
		"user_id", userID,
		"api_key_hash", apiKeyHash,
		"stream", norm.Stream,
		"message_count", len(norm.Messages),
	).Info("chat completion request processing")

	if norm.Stream {
		// Streaming: mid-stream fallback with per-step timeout/backoff/attempts
		steps := base.fallbackPlan(ctx)
		if len(steps) == 0 {
			steps = []FallbackStep{{Aliases: []string{model}, Strategy: "priority"}}
		} else {
			// Ensure primary alias is first in plan if absent
			hasPrimary := false
			for _, st := range steps {
				for _, al := range st.Aliases {
					if al == model {
						hasPrimary = true
						break
					}
				}
			}
			if !hasPrimary {
				steps = append([]FallbackStep{{Aliases: []string{model}, Strategy: "priority"}}, steps...)
			}
		}

		// Capture usage from the final streaming chunk for metrics recording
		var streamUsage *gw.Usage
		var streamModel string
		captureSend := func(c gw.ChatResponseChunk) error {
			if c.Usage != nil {
				streamUsage = c.Usage
			}
			if c.Model != "" {
				streamModel = c.Model
			}
			return send(toProtoResponseFromChunk(c))
		}
		recordStreamUsage := func() {
			if streamUsage != nil {
				streamProvider := base.getProviderForModel(ctx, streamModel)
				costDetails := metrics.CalculateCost(streamProvider, streamModel, streamUsage.PromptTokens, streamUsage.CompletionTokens, streamUsage.CacheReadCount())

				// Set attributes on root span for dashboard queries
				span.SetAttributes(
					attribute.String("model.served", streamModel),
					attribute.String("provider", streamProvider),
				)
				attrs.SetLLMTokens(span,
					int64(streamUsage.PromptTokens),
					int64(streamUsage.CompletionTokens),
					int64(streamUsage.TotalTokens),
				)
				attrs.SetCostDetails(span, costDetails.EstimatedUSD, costDetails.ActualUSD, costDetails.SavingsUSD, costDetails.CarbonSavedGrams, costDetails.PricingModel)

				// Meter the streamed attempt against the wallet (platform-key only).
				meter.record(streamUsage.KeySource, costDetails.EstimatedUSD)

				base.recordUsageMetrics(
					int64(streamUsage.PromptTokens),
					int64(streamUsage.CompletionTokens),
					costDetails.EstimatedUSD,
					0,     // No cache savings
					false, // Not a cache hit
				)
			}
		}

		var lastErr error
		for _, step := range steps {
			for _, alias := range step.Aliases {
				attempts := step.MaxAttempts
				if attempts <= 0 {
					attempts = 1
				}
				for a := 1; a <= attempts; a++ {
					start := time.Now()
					tx := ctx
					var cancel context.CancelFunc = func() {}
					if step.TimeoutMs > 0 {
						tx, cancel = context.WithTimeout(ctx, time.Duration(step.TimeoutMs)*time.Millisecond)
					}
					r := requestForModel(alias)
					err := gw.HandleChatStream(tx, base.routerFor(ctx), r, captureSend)
					elapsed := time.Since(start)
					if cancel != nil {
						cancel()
					}
					if err == nil {
						logger.WithFields(
							"metric", "gateway_stream_success_latency_ms",
							"alias", alias,
							"attempt", a,
							"elapsed_ms", elapsed.Milliseconds(),
							"correlation_id", correlationID,
							"requested_model", model,
							"actual_model", alias,
							"user_id", userID,
							"api_key_hash", apiKeyHash,
						).Info("chat completion succeeded")

						recordStreamUsage()
						return nil
					}
					lastErr = err
					logger.WithFields(
						"alias", alias,
						"attempt", a,
						"elapsed_ms", elapsed.Milliseconds(),
						"retryable", retrypolicy.IsRetryable(err),
					).Warn("stream attempt failed")

					// If auth error on first attempt, try key rotation before giving up on this alias
					if a == 1 && retrypolicy.IsAuthenticationError(err) {
						logger.WithCategory(logger.CategoryOperational).SetFields("alias", alias, "error", err.Error()).Info("gateway: stream auth error, attempting key rotation")
						maxKeyRotations := 10
						for keyRotation := 0; keyRotation < maxKeyRotations; keyRotation++ {
							rotated, currentKeyID := base.attemptKeyRotation(ctx, alias)
							if !rotated {
								break
							}
							if currentKeyID != "" {
								base.markFailedKey(currentKeyID)
							}

							// Retry with rotated key
							logger.WithCategory(logger.CategoryOperational).SetFields("alias", alias, "rotation_attempt", keyRotation+1).Info("gateway: stream retrying with rotated key")
							tx2 := ctx
							var cancel2 context.CancelFunc = func() {}
							if step.TimeoutMs > 0 {
								tx2, cancel2 = context.WithTimeout(ctx, time.Duration(step.TimeoutMs)*time.Millisecond)
							}
							r2 := requestForModel(alias)
							err = gw.HandleChatStream(tx2, base.routerFor(ctx), r2, captureSend)
							if cancel2 != nil {
								cancel2()
							}

							if err == nil {
								if currentKeyID != "" {
									base.clearFailedKey(currentKeyID)
								}
								logger.WithCategory(logger.CategoryOperational).SetFields("alias", alias, "rotation_attempt", keyRotation+1).Info("gateway: stream success after key rotation")
								recordStreamUsage()
								return nil
							}

							if !retrypolicy.IsAuthenticationError(err) {
								break // Different error, stop rotating
							}
							logger.WithCategory(logger.CategoryOperational).SetFields("alias", alias, "rotation_attempt", keyRotation+1).Info("gateway: stream key rotation failed")
						}
						// After all rotations, update lastErr and break to next alias
						lastErr = err
						break
					}

					if !retrypolicy.IsRetryable(err) || a == attempts {
						break
					}
					if step.BackoffMs > 0 {
						time.Sleep(time.Duration(step.BackoffMs) * time.Millisecond)
					}
				}
			}
		}
		if lastErr != nil {
			return lastErr
		}
		return nil
	}
	// Unary: try primary, then fallbacks per plan
	logger.WithFields(
		"lb_selected_model", model,
		"lb_strategy", base.cfg.LoadBalancer.Strategy,
		"lb_key_source", base.cfg.LoadBalancer.KeySource,
		"fallback_chain", base.fallbackAliasOrder(ctx),
	).Debug("gateway: primary selection")

	// Fast-path: Check caches for non-streaming requests
	isStreaming := req.Stream != nil && *req.Stream
	logger.WithFields(
		"is_streaming", isStreaming,
		"correlation_id", correlation.GetCorrelationID(ctx),
	).Debug("Fast-path: checking if streaming")

	if !isStreaming {
		engine := fastpath.EngineFromContext(ctx)
		logger.WithFields(
			"engine_nil", engine == nil,
			"engine_enabled", engine != nil && engine.IsEnabled(),
			"correlation_id", correlation.GetCorrelationID(ctx),
		).Debug("Fast-path: engine check")

		if engine != nil && engine.IsEnabled() {
			// Start unified cache lookup span
			cacheStartTime := time.Now()
			cacheCtx, cacheSpan := telemetry.StartUnifiedCacheLookupSpan(ctx)

			// Add cache lookup start event
			telemetry.AddSpanEvent(cacheSpan, attrs.EventCacheLookupStart,
				attribute.Bool("exact_enabled", true),
				attribute.Bool("semantic_enabled", engine.IsSemanticCacheEnabled()))

			logger.WithFields(
				"correlation_id", correlation.GetCorrelationID(ctx),
				"semantic_enabled", engine.IsSemanticCacheEnabled(),
			).Info("Fast-path: checking caches")

			var exactChecked, exactHit, semanticChecked, semanticHit bool
			var exactDurationMs, semanticDurationMs int64

			// 1. Check exact cache first (fastest)
			exactStart := time.Now()
			exactChecked = true
			telemetry.AddSpanEvent(cacheSpan, attrs.EventCacheExactLookup)

			if cached, found := engine.GetCachedResponse(&grpcRequestAdapter{req}); found {
				exactHit = true
				exactDurationMs = time.Since(exactStart).Milliseconds()
				engine.RecordFastPathRequest()

				// Unmarshal cached response
				var cachedResp gw.ChatCompletionResponse
				if err := fastpath.Unmarshal(cached.Response, &cachedResp); err == nil {
					if !isValidCachedChatResponse(cachedResp) {
						logger.WithFields(
							"correlation_id", correlation.GetCorrelationID(ctx),
							"cache_type", "exact",
							"model", cachedResp.Model,
							"response_id", cachedResp.ID,
						).Warn("Fast-path: ignoring invalid cached chat response")
						exactHit = false
						goto exactCacheMiss
					}
					// Add cache headers to response
					if md, ok := metadata.FromIncomingContext(ctx); ok {
						md.Set("x-cache", "HIT")
						md.Set("x-cache-type", "exact")
					}

					// Set cache lookup details on cache span
					cacheDurationMs := time.Since(cacheStartTime).Milliseconds()
					attrs.SetCacheLookupDetails(cacheSpan, true, true, "exact", cacheDurationMs,
						exactChecked, exactHit, exactDurationMs,
						false, false, 0, 0, 0, 0)

					// Add cache hit event
					telemetry.AddSpanEvent(cacheSpan, attrs.EventCacheLookupHit,
						attribute.String("cache_type", "exact"))
					cacheSpan.SetStatus(codes.Ok, "cache hit")
					cacheSpan.End()

					// === RECORD FULL METRICS FOR CACHE HIT ===
					// Calculate cost savings (the cost we avoided by using cache)
					cachedProvider := base.getProviderForModel(ctx, cachedResp.Model)
					costSaved := metrics.CalculateCost(cachedProvider, cachedResp.Model,
						cachedResp.Usage.PromptTokens, cachedResp.Usage.CompletionTokens, 0)

					// Set cost details - actual cost is 0, savings is what we would have paid
					attrs.SetCostDetails(span, 0, 0, costSaved.EstimatedUSD, costSaved.CarbonSavedGrams, "cached")

					// Record Prometheus metrics for cache hit (cost savings, tokens still counted)
					fpmetrics.RecordTokens(int64(cachedResp.Usage.PromptTokens), int64(cachedResp.Usage.CompletionTokens))
					fpmetrics.RecordCost(0, 0, costSaved.EstimatedUSD)

					// Record usage metrics to license monitor (cache hit)
					base.recordUsageMetrics(
						int64(cachedResp.Usage.PromptTokens),
						int64(cachedResp.Usage.CompletionTokens),
						0, // No actual cost for cache hit
						costSaved.EstimatedUSD,
						true, // Cache hit
					)

					// Set root span business metrics
					latencyMs := time.Since(startTime).Milliseconds()
					attrs.SetRootSpanBusinessMetrics(span,
						0, 0, 0, // cost input/output/total (0 for cached)
						costSaved.EstimatedUSD,                   // cost savings
						0,                                        // carbon grams
						costSaved.CarbonSavedGrams,               // carbon saved
						int64(cachedResp.Usage.PromptTokens),     // tokens input
						int64(cachedResp.Usage.CompletionTokens), // tokens output
						int64(cachedResp.Usage.TotalTokens),      // tokens total
						latencyMs,                                // total latency
						latencyMs,                                // TTFT (same as latency for cache)
						0,                                        // provider latency (0 for cache)
					)

					// Set token usage
					attrs.SetLLMTokens(span, int64(cachedResp.Usage.PromptTokens), int64(cachedResp.Usage.CompletionTokens), int64(cachedResp.Usage.TotalTokens))

					// Set response details
					if len(cachedResp.Choices) > 0 {
						responseText := extractTextFromContentParts(cachedResp.Choices[0].Message.Content)
						attrs.SetResponseDetails(span, responseText, cachedResp.Choices[0].FinishReason, cachedResp.Model, cachedResp.Usage.CompletionTokens, len(responseText))
					}

					// Set cache hit attributes
					span.SetAttributes(
						attribute.Bool("cache.hit", true),
						attribute.String("cache.type", "exact"),
						attribute.String("response.model", cachedResp.Model),
						attribute.String("response.id", cachedResp.ID),
					)

					// Log cache hit with full details
					logger.WithFields(
						"correlation_id", correlation.GetCorrelationID(ctx),
						"cache_type", "exact",
						"model", cachedResp.Model,
						"tokens_total", cachedResp.Usage.TotalTokens,
						"cost_saved_usd", costSaved.EstimatedUSD,
						"latency_ms", latencyMs,
					).Info("Cache HIT - request served from exact cache")

					span.SetStatus(codes.Ok, "cache hit - exact")

					return send(toProtoResponse(cachedResp))
				}
			}
		exactCacheMiss:
			exactDurationMs = time.Since(exactStart).Milliseconds()

			// 2. Check semantic cache (similarity-based)
			logger.WithFields(
				"semantic_enabled", engine.IsSemanticCacheEnabled(),
				"correlation_id", correlation.GetCorrelationID(ctx),
			).Info("Fast-path: about to check semantic cache")

			if engine.IsSemanticCacheEnabled() {
				queryText := extractQueryTextFromProto(req)
				logger.WithFields(
					"query_text", queryText,
					"query_text_empty", queryText == "",
					"correlation_id", correlation.GetCorrelationID(ctx),
				).Info("Fast-path: extracted query text")

				if queryText != "" {
					semanticStart := time.Now()
					semanticChecked = true

					// Add semantic lookup event
					telemetry.AddSpanEvent(cacheSpan, attrs.EventCacheSemanticLookup,
						attribute.Int("query_length", len(queryText)))

					logger.WithFields(
						"query_text", queryText,
						"correlation_id", correlation.GetCorrelationID(ctx),
					).Info("Checking semantic cache")

					if cached, found := engine.GetSemanticCachedResponseWithContext(cacheCtx, queryText); found {
						semanticHit = true
						semanticDurationMs = time.Since(semanticStart).Milliseconds()
						engine.RecordFastPathRequest()

						// Unmarshal cached response
						var cachedResp gw.ChatCompletionResponse
						if err := fastpath.Unmarshal(cached.Response, &cachedResp); err == nil {
							if !isValidCachedChatResponse(cachedResp) {
								logger.WithFields(
									"correlation_id", correlation.GetCorrelationID(ctx),
									"cache_type", "semantic",
									"model", cachedResp.Model,
									"response_id", cachedResp.ID,
									"query_text", queryText,
								).Warn("Fast-path: ignoring invalid cached chat response")
								semanticHit = false
								semanticDurationMs = time.Since(semanticStart).Milliseconds()
							} else {
								// Add cache headers to response
								if md, ok := metadata.FromIncomingContext(ctx); ok {
									md.Set("x-cache", "HIT")
									md.Set("x-cache-type", "semantic")
								}

								// Set cache lookup details on cache span
								cacheDurationMs := time.Since(cacheStartTime).Milliseconds()
								attrs.SetCacheLookupDetails(cacheSpan, true, true, "semantic", cacheDurationMs,
									exactChecked, exactHit, exactDurationMs,
									semanticChecked, semanticHit, semanticDurationMs, 0, 0, 0)

								// Add cache hit event
								telemetry.AddSpanEvent(cacheSpan, attrs.EventCacheLookupHit,
									attribute.String("cache_type", "semantic"))
								cacheSpan.SetStatus(codes.Ok, "semantic cache hit")
								cacheSpan.End()

								// === RECORD FULL METRICS FOR CACHE HIT ===
								// Calculate cost savings (the cost we avoided by using cache)
								semanticCachedProvider := base.getProviderForModel(ctx, cachedResp.Model)
								costSaved := metrics.CalculateCost(semanticCachedProvider, cachedResp.Model,
									cachedResp.Usage.PromptTokens, cachedResp.Usage.CompletionTokens, 0)

								// Set cost details - actual cost is 0, savings is what we would have paid
								attrs.SetCostDetails(span, 0, 0, costSaved.EstimatedUSD, costSaved.CarbonSavedGrams, "cached")

								// Record Prometheus metrics for semantic cache hit
								fpmetrics.RecordTokens(int64(cachedResp.Usage.PromptTokens), int64(cachedResp.Usage.CompletionTokens))
								fpmetrics.RecordCost(0, 0, costSaved.EstimatedUSD)

								// Record usage metrics to license monitor (semantic cache hit)
								base.recordUsageMetrics(
									int64(cachedResp.Usage.PromptTokens),
									int64(cachedResp.Usage.CompletionTokens),
									0, // No actual cost for cache hit
									costSaved.EstimatedUSD,
									true, // Cache hit
								)

								// Set root span business metrics
								latencyMs := time.Since(startTime).Milliseconds()
								attrs.SetRootSpanBusinessMetrics(span,
									0, 0, 0, // cost input/output/total (0 for cached)
									costSaved.EstimatedUSD,                   // cost savings
									0,                                        // carbon grams
									costSaved.CarbonSavedGrams,               // carbon saved
									int64(cachedResp.Usage.PromptTokens),     // tokens input
									int64(cachedResp.Usage.CompletionTokens), // tokens output
									int64(cachedResp.Usage.TotalTokens),      // tokens total
									latencyMs,                                // total latency
									latencyMs,                                // TTFT (same as latency for cache)
									0,                                        // provider latency (0 for cache)
								)

								// Set token usage
								attrs.SetLLMTokens(span, int64(cachedResp.Usage.PromptTokens), int64(cachedResp.Usage.CompletionTokens), int64(cachedResp.Usage.TotalTokens))

								// Set response details
								if len(cachedResp.Choices) > 0 {
									responseText := extractTextFromContentParts(cachedResp.Choices[0].Message.Content)
									attrs.SetResponseDetails(span, responseText, cachedResp.Choices[0].FinishReason, cachedResp.Model, cachedResp.Usage.CompletionTokens, len(responseText))
								}

								// Set cache hit attributes
								span.SetAttributes(
									attribute.Bool("cache.hit", true),
									attribute.String("cache.type", "semantic"),
									attribute.String("response.model", cachedResp.Model),
									attribute.String("response.id", cachedResp.ID),
								)

								// Log cache hit with full details
								logger.WithFields(
									"correlation_id", correlation.GetCorrelationID(ctx),
									"cache_type", "semantic",
									"model", cachedResp.Model,
									"tokens_total", cachedResp.Usage.TotalTokens,
									"cost_saved_usd", costSaved.EstimatedUSD,
									"latency_ms", latencyMs,
									"query_text", queryText,
								).Info("Cache HIT - request served from semantic cache")

								span.SetStatus(codes.Ok, "cache hit - semantic")

								return send(toProtoResponse(cachedResp))
							}
						} else {
							logger.WithError(err).Warn("Failed to unmarshal semantic cache response")
						}
					} else {
						semanticDurationMs = time.Since(semanticStart).Milliseconds()
						logger.WithFields(
							"query_text", queryText,
							"correlation_id", correlation.GetCorrelationID(ctx),
						).Debug("Semantic cache MISS")
					}
				}
			}

			// Cache miss - set final attributes and add miss event
			cacheDurationMs := time.Since(cacheStartTime).Milliseconds()
			attrs.SetCacheLookupDetails(cacheSpan, true, false, "none", cacheDurationMs,
				exactChecked, exactHit, exactDurationMs,
				semanticChecked, semanticHit, semanticDurationMs, 0, 0, 0)

			telemetry.AddSpanEvent(cacheSpan, attrs.EventCacheLookupMiss,
				attribute.Bool("exact_checked", exactChecked),
				attribute.Bool("semantic_checked", semanticChecked))
			cacheSpan.End()
		}
	}

	requestedModel := model // Store for fallback tracking
	resp, err := gw.HandleChat(ctx, base.routerFor(ctx), norm)

	// Tool Loop: Execute serverless functions if response contains tool calls
	// This loop continues until the LLM returns a response without tool calls
	// or the maximum iteration limit is reached
	toolLoopIterations := 0
	maxToolLoopIterations := 10 // Safety limit to prevent infinite loops
	// Get org ID from license monitor (gateway is single-tenant, associated with one org)
	toolLoopOrgID := base.GetOrganizationID()

	for err == nil && base.ShouldExecuteToolLoop(&resp) && toolLoopIterations < maxToolLoopIterations {
		toolLoopIterations++

		toolCallCount := 0
		if len(resp.Choices) > 0 {
			toolCallCount = len(resp.Choices[0].Message.ToolCalls)
		}

		logger.WithFields(
			"correlation_id", correlationID,
			"iteration", toolLoopIterations,
			"tool_calls", toolCallCount,
		).Info("gateway: executing tool loop iteration")

		// Execute tools and get result messages
		toolMessages, toolErr := base.ExecuteToolLoop(ctx, toolLoopOrgID, correlationID, correlationID, &resp)
		if toolErr != nil {
			logger.WithFields(
				"correlation_id", correlationID,
				"error", toolErr.Error(),
			).Warn("gateway: tool loop execution failed, continuing with error in response")
		}

		if len(toolMessages) == 0 {
			// No tool messages to add, break the loop
			break
		}

		// Append tool messages (assistant message with tool_calls + tool results) to conversation
		norm.Messages = append(norm.Messages, toolMessages...)

		// Clear tool_choice after first tool execution to allow the LLM to respond with text
		// If tool_choice was "required", the LLM would keep calling tools forever
		norm.ToolChoice = nil

		// Re-invoke the LLM with the updated conversation
		resp, err = gw.HandleChat(ctx, base.routerFor(ctx), norm)

		// Add telemetry for tool loop iteration
		span.AddEvent("tool_loop_iteration", trace.WithAttributes(
			attribute.Int("iteration", toolLoopIterations),
			attribute.Int("tool_messages_added", len(toolMessages)),
		))
	}

	if toolLoopIterations > 0 {
		logger.WithFields(
			"correlation_id", correlationID,
			"total_iterations", toolLoopIterations,
		).Info("gateway: tool loop completed")

		span.SetAttributes(
			attribute.Int("tool_loop.iterations", toolLoopIterations),
			attribute.Bool("tool_loop.executed", true),
		)
	}

	if err == nil {
		// Success - process response with tracing
		respStart := time.Now()
		_, respSpan := telemetry.StartResponseProcessingSpan(ctx)

		latencyMs := time.Since(startTime).Milliseconds()
		respProvider := base.getProviderForModel(ctx, resp.Model)
		span.SetAttributes(
			attribute.String("model.served", resp.Model),
			attribute.String("provider", respProvider),
			attribute.Int64("latency_ms", latencyMs),
			attribute.Bool("fallback.triggered", false),
		)

		// Record token usage if available
		if resp.Usage.PromptTokens > 0 || resp.Usage.CompletionTokens > 0 {
			// Record token usage on response processing span
			attrs.SetLLMTokens(respSpan,
				int64(resp.Usage.PromptTokens),
				int64(resp.Usage.CompletionTokens),
				int64(resp.Usage.TotalTokens),
			)
			// Also record on root span for dashboard aggregation
			attrs.SetLLMTokens(span,
				int64(resp.Usage.PromptTokens),
				int64(resp.Usage.CompletionTokens),
				int64(resp.Usage.TotalTokens),
			)
		}

		// Use centralized setter for response processing
		respMetadata := attrs.AnalyzeResponse(resp)
		respDuration := time.Since(respStart).Milliseconds()
		attrs.SetResponseProcessing(respSpan, resp.Model, resp.ID, respMetadata, respDuration)

		// Add detailed response attributes
		outputJSON := attrs.SerializeToJSON(resp.Choices)
		finishReason := ""
		if len(resp.Choices) > 0 {
			finishReason = resp.Choices[0].FinishReason
		}
		attrs.SetResponseDetails(respSpan, outputJSON, finishReason, resp.Model,
			resp.Usage.CompletionTokens, len(outputJSON))

		// Add span event for response complete
		telemetry.AddSpanEvent(respSpan, attrs.EventResponseComplete,
			attribute.Int("response.tokens", resp.Usage.CompletionTokens),
			attribute.Bool("response.cached", false))

		respSpan.SetStatus(codes.Ok, "response processed")
		respSpan.End()

		// Capture output at trace level (root span)
		attrs.SetTraceOutput(span, resp.Choices)

		// Calculate and set cost details using the resolved provider (respProvider resolved above)
		costDetails := metrics.CalculateCost(respProvider, resp.Model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.CacheReadCount())
		attrs.SetCostDetails(span, costDetails.EstimatedUSD, costDetails.ActualUSD, costDetails.SavingsUSD, costDetails.CarbonSavedGrams, costDetails.PricingModel)

		// Meter the served attempt against the wallet (platform-key only).
		meter.record(resp.KeySource, costDetails.EstimatedUSD)

		// Record Prometheus metrics for tokens and cost
		fpmetrics.RecordTokens(int64(resp.Usage.PromptTokens), int64(resp.Usage.CompletionTokens))
		inputCostRatio := 0.3 // Roughly 30% input, 70% output for most models
		fpmetrics.RecordCost(costDetails.EstimatedUSD*inputCostRatio, costDetails.EstimatedUSD*(1-inputCostRatio), 0)

		// Record usage metrics to license monitor (provider call, no cache)
		// This may return a SpendLimitExceededError if the limit is now exceeded
		spendErr := base.recordUsageMetrics(
			int64(resp.Usage.PromptTokens),
			int64(resp.Usage.CompletionTokens),
			costDetails.EstimatedUSD,
			0,     // No cache savings
			false, // Cache miss
		)
		// If spend limit exceeded, we still return the response (can't undo the request)
		// but log prominently that the limit was exceeded
		if spendErr != nil && licensemonitor.IsSpendLimitExceeded(spendErr) {
			logger.Errorf("gateway: SPEND LIMIT EXCEEDED during request - this request completed but subsequent requests will be blocked: %v", spendErr)
		}

		// Set root span business metrics for easy querying
		// Estimate input/output cost breakdown (rough estimate based on typical pricing)
		attrs.SetRootSpanBusinessMetrics(span,
			costDetails.EstimatedUSD*inputCostRatio,     // cost input
			costDetails.EstimatedUSD*(1-inputCostRatio), // cost output
			costDetails.EstimatedUSD,                    // cost total
			costDetails.SavingsUSD,                      // cost savings
			0,                                           // carbon grams (would need calculation)
			costDetails.CarbonSavedGrams,                // carbon saved
			int64(resp.Usage.PromptTokens),              // tokens input
			int64(resp.Usage.CompletionTokens),          // tokens output
			int64(resp.Usage.TotalTokens),               // tokens total
			latencyMs,                                   // total latency
			0,                                           // TTFT (not available in unary)
			latencyMs,                                   // provider latency
		)

		// Calculate and set performance metrics
		p50, p95, p99, _ := getPerformancePercentiles()
		throughputTPS := 0.0
		if latencyMs > 0 {
			throughputTPS = float64(resp.Usage.TotalTokens) / (float64(latencyMs) / 1000.0)
		}
		latencyCategory := categorizeLatency(float64(latencyMs))
		attrs.SetPerformanceMetrics(span, float64(latencyMs), p50, p95, p99, throughputTPS, latencyCategory)

		// Classify and set business metrics
		businessMetrics := classifyBusinessMetrics(norm.Messages, resp)
		attrs.SetBusinessMetrics(span, businessMetrics.UseCase, businessMetrics.Domain, businessMetrics.QueryType, businessMetrics.QualityScore)

		span.SetStatus(codes.Ok, "success")

		// Fast-path: Cache the response for future requests (async)
		if !isStreaming {
			if engine := fastpath.EngineFromContext(ctx); engine != nil && engine.IsEnabled() {
				corrID := correlation.GetCorrelationID(ctx)

				// Extract trace context before goroutine to maintain tracing
				// Create a detached context that won't be cancelled when request completes
				traceCtx := telemetry.GetTraceContext(ctx)
				parentSpanContext := span.SpanContext()

				go func() {
					if !isValidCachedChatResponse(resp) {
						logger.WithFields(
							"correlation_id", corrID,
							"model", resp.Model,
							"response_id", resp.ID,
						).Warn("Fast-path: skipping cache store for invalid chat response")
						return
					}
					// Create new context with trace information for async operation
					// Use ContextWithRemoteSpanContext to properly link to parent span
					asyncCtx := trace.ContextWithRemoteSpanContext(context.Background(), parentSpanContext)
					asyncCtx = correlation.WithCorrelationID(asyncCtx, corrID)
					asyncCtx = telemetry.WithTraceContext(asyncCtx, traceCtx)

					// Start async cache storage span as a child of the parent span
					asyncCtx, asyncSpan := telemetry.StartCacheStoreSpan(asyncCtx, "async")
					defer asyncSpan.End()

					logger.WithFields("correlation_id", corrID).Info("Fast-path: starting async cache storage")

					respBytes, err := fastpath.Marshal(resp)
					if err == nil {
						cachedResp := &cache.CachedResponse{
							Response:     respBytes,
							Model:        resp.Model,
							OutputTokens: resp.Usage.CompletionTokens,
						}

						// Store in exact cache with context
						engine.CacheResponseWithContext(asyncCtx, &grpcRequestAdapter{req}, cachedResp)
						logger.WithFields("correlation_id", corrID).Info("Stored response in exact cache")

						// Also store in semantic cache for similarity matching
						if engine.IsSemanticCacheEnabled() {
							queryText := extractQueryTextFromProto(req)
							logger.WithFields(
								"correlation_id", corrID,
								"query_text", queryText,
								"query_empty", queryText == "",
							).Info("Fast-path: about to store in semantic cache")

							if queryText != "" {
								engine.CacheSemanticResponseWithContext(asyncCtx, queryText, cachedResp)
								logger.WithFields(
									"correlation_id", corrID,
									"query_text", queryText,
								).Info("Stored response in semantic cache")
							}
						} else {
							logger.WithFields("correlation_id", corrID).Warn("Semantic cache not enabled during storage")
						}

						asyncSpan.SetStatus(codes.Ok, "cache storage completed")
					} else {
						logger.WithError(err).WithField("correlation_id", corrID).Warn("Failed to marshal response for caching")
						telemetry.RecordError(asyncSpan, err)
					}
				}()
			}
		}

		return send(toProtoResponse(resp))
	}

	// Check if this is a model not found error - these should NOT trigger fallback
	// Model not found means configuration issue, not a transient error
	// NOTE: With early validation (line 95), this should rarely be reached for model-not-found
	// but serves as a safety net for edge cases
	if gw.IsNonRetriableError(err) {
		logger.WithFields(
			"model", model,
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("gateway: non-retriable error - failing fast without fallback")

		// Only emit model.not_found event if it's actually a model-not-found error
		// (not other non-retriable errors like malformed requests)
		if gw.IsModelNotFoundError(err) {
			userID := base.extractUserID(ctx)
			apiKeyHash := chat.HashAPIKey(base.extractAPIKey(ctx))
			base.emitModelNotFoundEvent(ctx, model, userID, apiKeyHash, correlationID)
		}

		// Record error on span
		telemetry.RecordError(span, err)
		span.SetStatus(codes.Error, "non-retriable error")

		return connect.NewError(connect.CodeNotFound, err)
	}

	// If auth error, attempt key rotation before provider fallback
	if retrypolicy.IsAuthenticationError(err) {
		logger.WithCategory(logger.CategoryOperational).SetFields("model", model, "error", err.Error()).Info("gateway: authentication error detected, attempting key rotation")

		// Add key rotation start event
		telemetry.AddSpanEvent(span, attrs.EventKeyRotationStart,
			attribute.String("provider", telemetry.GetProviderForModel(model)),
			attribute.String("reason", "auth_error"))

		// Try rotating through all available keys for this provider
		maxKeyRotations := 10 // Prevent infinite loop
		keysTried := 0
		rotationSuccess := false
		keyRotationStart := time.Now()

		for keyRotation := 0; keyRotation < maxKeyRotations; keyRotation++ {
			rotated, currentKeyID := base.attemptKeyRotation(ctx, model)
			if !rotated {
				logger.WithCategory(logger.CategoryOperational).Info("gateway: no more keys available for rotation, proceeding to provider fallback")
				break
			}
			keysTried++

			// Mark the current key as failed
			if currentKeyID != "" {
				base.markFailedKey(currentKeyID)
			}

			// Retry with rotated provider
			logger.WithCategory(logger.CategoryOperational).SetFields("model", model, "rotation_attempt", keyRotation+1).Info("gateway: retrying with rotated key")
			resp, err = gw.HandleChat(ctx, base.routerFor(ctx), norm)
			if err == nil {
				// Success! Clear the failed key marker for next request
				if currentKeyID != "" {
					base.clearFailedKey(currentKeyID)
				}
				rotationSuccess = true

				// Record key rotation metrics
				attrs.SetKeyRotationMetrics(span, keyRotation+1, "auth_error", true, maxKeyRotations-keysTried, keysTried, time.Since(keyRotationStart).Milliseconds())
				telemetry.AddSpanEvent(span, attrs.EventKeyRotationComplete,
					attribute.Bool("success", true),
					attribute.Int("keys_tried", keysTried))

				return send(toProtoResponse(resp))
			}

			// Check if still auth error
			if !retrypolicy.IsAuthenticationError(err) {
				logger.WithCategory(logger.CategoryOperational).SetFields("model", model, "error", err.Error()).Info("gateway: non-auth error after key rotation")
				break // Different error type, proceed to fallback
			}

			logger.WithCategory(logger.CategoryOperational).SetFields("model", model, "rotation_attempt", keyRotation+1, "error", err.Error()).Info("gateway: key rotation attempt failed")
		}

		// Record key rotation completion if we exhausted keys without success
		if !rotationSuccess && keysTried > 0 {
			attrs.SetKeyRotationMetrics(span, keysTried, "auth_error", false, 0, keysTried, time.Since(keyRotationStart).Milliseconds())
			telemetry.AddSpanEvent(span, attrs.EventKeyRotationComplete,
				attribute.Bool("success", false),
				attribute.Int("keys_tried", keysTried))
		}
	}

	// Build fallback plan (supports priority, round_robin, parallel)
	plan := base.fallbackPlan(ctx)
	fallbackAttempts := int32(0)
	fallbackStartTime := time.Now()

	// Mark that fallback was triggered using proper event
	span.SetAttributes(attribute.Bool("fallback.triggered", true))
	telemetry.AddSpanEvent(span, attrs.EventFallbackStarted,
		attribute.String("reason", "primary model failed"),
		attribute.String("primary_model", requestedModel),
		attribute.Int("fallback_options", len(plan)))

	// User info already extracted earlier for OTEL logging

	for _, step := range plan {
		if len(step.Aliases) == 0 {
			continue
		}
		if step.Strategy == "parallel" && len(step.Aliases) > 1 {
			// Race all aliases; return first success
			var tx context.Context = ctx
			var cancel context.CancelFunc = func() {}
			if step.TimeoutMs > 0 {
				tx, cancel = context.WithTimeout(ctx, time.Duration(step.TimeoutMs)*time.Millisecond)
			} else {
				tx, cancel = context.WithCancel(ctx)
			}
			defer cancel()
			resCh := make(chan gw.ChatCompletionResponse, 1)
			errCh := make(chan error, len(step.Aliases))
			cfg := telemetry.GetGlobalTracingConfig()
			for i, alias := range step.Aliases {
				al := alias
				attemptNum := int(fallbackAttempts) + i + 1
				go func(attemptIdx int) {
					r := requestForModel(al)

					// Create fallback span for this parallel attempt if tracing is enabled
					var fallbackCtx context.Context = tx
					var fallbackSpan trace.Span
					if cfg != nil && cfg.TraceFallbacks {
						fallbackCtx, fallbackSpan = telemetry.StartFallbackSpan(tx, attemptIdx, step.Name+" (parallel)",
							telemetry.WithRequestID(correlationID),
							telemetry.WithModel(al))
						defer fallbackSpan.End()
					}

					if rr, e := gw.HandleChat(fallbackCtx, base.routerFor(ctx), r); e == nil {
						if fallbackSpan != nil {
							fallbackSpan.SetStatus(codes.Ok, "parallel fallback succeeded")
						}
						select {
						case resCh <- rr:
						default:
						}
					} else {
						if fallbackSpan != nil {
							telemetry.RecordError(fallbackSpan, e)
						}
						errCh <- e
					}
				}(attemptNum)
			}
			select {
			case rr := <-resCh:
				cancel()
				fallbackAttempts++
				durationMs := time.Since(fallbackStartTime).Milliseconds()
				logger.WithFields(
					"requested_model", requestedModel,
					"fallback_model", rr.Model,
					"fallback_reason", step.Name,
					"attempts", fallbackAttempts,
					"correlation_id", correlationID,
				).Warn("gateway: fallback succeeded (parallel strategy)")

				// Emit fallback succeeded event
				base.emitFallbackSucceededEvent(ctx, requestedModel, rr.Model, step.Name, fallbackAttempts, userID, apiKeyHash, correlationID, durationMs)

				// Record fallback success on main span
				span.SetAttributes(
					attribute.String("model.served", rr.Model),
					attribute.String("provider", base.getProviderForModel(ctx, rr.Model)),
					attribute.Int("fallback.attempts", int(fallbackAttempts)),
				)
				span.SetStatus(codes.Ok, "fallback succeeded")

				return send(attachFallbackInfo(toProtoResponse(rr), requestedModel, rr.Model, step.Name, fallbackAttempts))
			case <-tx.Done():
				// timeout/cancel without success; proceed to next step
				cancel()
			}
			continue
		}
		// priority / round_robin already expanded into single-alias steps
		for _, alias := range step.Aliases {
			if alias == "" || alias == model {
				continue
			}
			attempts := step.MaxAttempts
			if attempts <= 0 {
				attempts = 1
			}
			var last error
			for a := 1; a <= attempts; a++ {
				var tx context.Context = ctx
				var cancel context.CancelFunc = func() {}
				if step.TimeoutMs > 0 {
					tx, cancel = context.WithTimeout(ctx, time.Duration(step.TimeoutMs)*time.Millisecond)
				}
				start := time.Now()
				r := requestForModel(alias)

				// Create fallback span if tracing is enabled
				cfg := telemetry.GetGlobalTracingConfig()
				var fallbackSpan trace.Span
				if cfg != nil && cfg.TraceFallbacks {
					tx, fallbackSpan = telemetry.StartFallbackSpan(tx, int(fallbackAttempts)+1, step.Name,
						telemetry.WithRequestID(correlationID),
						telemetry.WithModel(alias))
					defer fallbackSpan.End()
				}

				if rr, e := gw.HandleChat(tx, base.routerFor(ctx), r); e == nil {
					if cancel != nil {
						cancel()
					}
					elapsed := time.Since(start)
					fallbackAttempts++
					durationMs := time.Since(fallbackStartTime).Milliseconds()
					logger.WithFields(
						"metric", "gateway_unary_success_latency_ms",
						"requested_model", requestedModel,
						"fallback_model", alias,
						"fallback_reason", step.Name,
						"alias", alias,
						"attempt", a,
						"elapsed_ms", elapsed.Milliseconds(),
						"correlation_id", correlationID,
					).Warn("gateway: fallback succeeded (priority/round_robin strategy)")

					// Emit fallback succeeded event
					base.emitFallbackSucceededEvent(ctx, requestedModel, rr.Model, step.Name, fallbackAttempts, userID, apiKeyHash, correlationID, durationMs)

					// Record fallback success on fallback span if it exists
					if fallbackSpan != nil {
						fallbackSpan.SetStatus(codes.Ok, "fallback succeeded")
					}

					// Record fallback success on main span
					span.SetAttributes(
						attribute.String("model.served", rr.Model),
						attribute.String("provider", base.getProviderForModel(ctx, rr.Model)),
						attribute.Int("fallback.attempts", int(fallbackAttempts)),
						attribute.Int64("latency_ms", elapsed.Milliseconds()),
					)
					span.SetStatus(codes.Ok, "fallback succeeded")

					return send(attachFallbackInfo(toProtoResponse(rr), requestedModel, rr.Model, step.Name, fallbackAttempts))
				} else {
					last = e
					if cancel != nil {
						cancel()
					}
					elapsed := time.Since(start)

					// Record error on fallback span if it exists
					if fallbackSpan != nil {
						telemetry.RecordError(fallbackSpan, e)
					}

					logger.WithFields(
						"alias", alias,
						"attempt", a,
						"elapsed_ms", elapsed.Milliseconds(),
						"retryable", retrypolicy.IsRetryable(e),
					).Warn("unary attempt failed")
					if !retrypolicy.IsRetryable(e) || a == attempts {
						break
					}
					if step.BackoffMs > 0 {
						time.Sleep(time.Duration(step.BackoffMs) * time.Millisecond)
					}
				}
			}
			if last == nil {
				continue
			}
		}
	}
	// All fallback attempts exhausted - emit failure event
	if fallbackAttempts > 0 {
		lastError := "all fallback models exhausted"
		if err != nil {
			lastError = err.Error()
		}
		base.emitFallbackFailedEvent(ctx, requestedModel, "fallback_exhausted", fallbackAttempts, userID, apiKeyHash, correlationID, lastError)
		logger.WithFields(
			"requested_model", requestedModel,
			"attempts", fallbackAttempts,
			"correlation_id", correlationID,
		).Error("gateway: all fallback attempts failed")

		// Record fallback failure on span
		span.SetAttributes(
			attribute.Int("fallback.attempts", int(fallbackAttempts)),
		)
	}

	// Record final error on span
	if err != nil {
		telemetry.RecordError(span, err)
		span.SetStatus(codes.Error, "request failed")
	}

	// return original error if all failed
	return err
}

func validateOutputTokenLimits(sampling *gatewaypb.SamplingParams) error {
	if sampling == nil {
		return nil
	}
	if sampling.MaxTokens != nil && sampling.GetMaxTokens() <= 0 {
		return errors.New("max_tokens must be greater than 0")
	}
	if sampling.MaxCompletionTokens != nil && sampling.GetMaxCompletionTokens() <= 0 {
		return errors.New("max_completion_tokens must be greater than 0")
	}
	return nil
}

// Shared handler for Embeddings across Connect & gRPC
func processEmbeddings(ctx context.Context, base *Server, req *gatewaypb.EmbeddingsRequest, send func(*gatewaypb.EmbeddingsResponse) error) error {
	// Per-tenant feature gate. The toggle in Settings → Configuration →
	// Features ("Embeddings") writes to runtime_config.features.enable_embeddings;
	// this is the consumer that actually enforces it. Fail-open if no
	// rtconfig in ctx (single-binary CE, tests).
	if rt := rtconfigFromCtx(ctx, base); rt != nil {
		if !rt.IsEmbeddingsEnabled(contextkeys.ExtractTenantID(ctx)) {
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("embeddings are disabled for this tenant"))
		}
	}
	if err := base.EnsureProvidersForRequest(ctx); err != nil {
		logger.WithFields("error", err.Error()).Warn("gateway: failed to refresh tenant providers for embeddings request")
	}
	if base.routerFor(ctx) == nil {
		base.bootstrapFromConfig()
	}
	// Ensure a server-side correlation ID exists for this request (with emb_ prefix)
	ctx, _ = correlation.EnsureEndpointCorrelationID(ctx, correlation.EndpointEmbeddings)

	// Extract correlation ID and user context
	correlationID := correlation.GetCorrelationID(ctx)
	userID := base.extractUserID(ctx)
	apiKeyHash := base.extractAPIKeyHash(ctx)
	tenantID := base.extractTenantID(ctx)

	// Extract input text - from input field or from messages
	inputText := req.GetInput()
	if inputText == "" && len(req.GetMessages()) > 0 {
		// Extract text from messages (consistent with ChatCompletion format)
		var textParts []string
		for _, msg := range req.GetMessages() {
			if msg.Role == gatewaypb.Role_ROLE_USER {
				for _, part := range msg.Content {
					if part.Type == "text" {
						if text := part.GetText(); text != "" {
							textParts = append(textParts, text)
						}
					}
				}
			}
		}
		if len(textParts) > 0 {
			inputText = strings.Join(textParts, " ")
		}
	}

	// Start distributed trace span for embedding request
	ctx, span := telemetry.StartEmbeddingSpan(ctx, req.GetModel(), len(inputText),
		telemetry.WithRequestID(correlationID),
	)
	defer span.End()

	// Enrich trace context with metadata (similar to ChatCompletion)
	traceCtx := telemetry.GetTraceContext(ctx)
	traceCtx.TraceName = "Embeddings"
	traceCtx.UserID = userID
	// Prefer a caller-supplied session over the correlation-id fallback.
	embSessionID := base.extractSessionID(ctx, req.GetMetadata())
	if embSessionID == "" {
		embSessionID = correlationID
	}
	traceCtx.SessionID = embSessionID
	traceCtx.ThreadID = base.extractThreadID(ctx, req.GetMetadata())

	// Set environment and release from environment variables or defaults
	traceCtx.Environment = "development"
	if env := os.Getenv("DEPLOYMENT_ENV"); env != "" {
		traceCtx.Environment = env
	}
	if release := os.Getenv("APP_VERSION"); release != "" {
		traceCtx.Release = release
	}
	ctx = telemetry.WithTraceContext(ctx, traceCtx)

	// Add business context to span
	attrs.SetBusinessContext(span, userID, apiKeyHash, tenantID)

	startTime := time.Now()

	// Start normalization span
	normStart := time.Now()
	ctx, normSpan := telemetry.StartRequestNormalizationSpan(ctx)

	// Add span event for normalization start
	telemetry.AddSpanEvent(normSpan, attrs.EventNormalizationStart,
		attribute.String("input_format", "embeddings-compatible"))

	model := req.GetModel()
	if model == "" {
		model = base.selectOmittedModel(ctx)
		if model == "" {
			normSpan.End()
			err := connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("no default model configured - please specify a model in the request or configure a default provider"))
			span.RecordError(err)
			span.SetStatus(codes.Error, "no default model configured")
			return err
		}
	}

	// Validate input is not empty
	if inputText == "" {
		normSpan.End()
		err := connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("input text is required - provide 'input' field or 'messages' array"))
		span.RecordError(err)
		span.SetStatus(codes.Error, "input is required")
		return err
	}

	// Set normalization metadata
	normDuration := time.Since(normStart).Milliseconds()
	attrs.SetNormalizationMetadata(normSpan, normDuration, false)

	// Add span event for normalization complete
	telemetry.AddSpanEvent(normSpan, attrs.EventNormalizationComplete,
		attribute.Bool("output_valid", true))

	normSpan.End()

	// Add model to span
	span.SetAttributes(
		attribute.String("model.requested", model),
		attribute.String("llm.request.model", model),
		attribute.Int("llm.embeddings.input_length", len(inputText)),
	)

	// Truncate input for logging (max 500 chars)
	inputPreview := inputText
	if len(inputPreview) > 500 {
		inputPreview = inputPreview[:500] + "..."
	}

	// Set trace input (for trace-level visibility)
	span.SetAttributes(
		attribute.String("llm.input.preview", inputPreview),
		attribute.Int("llm.input.chars", len(inputText)),
	)

	// Set request details on root span
	attrs.SetRequestDetails(span, correlationID, inputPreview, "", "", len(inputText), len(inputText))

	// Log request received with input preview
	logger.WithFields(
		"correlation_id", correlationID,
		"model", model,
		"user_id", userID,
		"api_key_hash", apiKeyHash,
		"input_length", len(inputText),
		"input_preview", inputPreview,
	).Info("embeddings: request received")

	// Model resolution span
	resolveStart := time.Now()
	ctx, resolveSpan := telemetry.StartModelResolutionSpan(ctx, model)

	// resolvedProvider carries the provider resolved here so the cost calc and
	// root span below reuse it instead of re-resolving via getProviderForModel.
	var resolvedProvider string

	// Validate model exists BEFORE calling provider
	if base.routerFor(ctx) != nil {
		provider, actualModel, err := base.routerFor(ctx).ResolveWithContext(ctx, model)
		resolveDuration := time.Since(resolveStart).Milliseconds()

		if err != nil {
			resolveSpan.RecordError(err)
			resolveSpan.SetStatus(codes.Error, "model not found")
			resolveSpan.End()

			span.RecordError(err)
			span.SetStatus(codes.Error, "model not found")
			return connect.NewError(connect.CodeNotFound, err)
		}

		// Prefer the resolved route's provider name over the provider object's
		// Name() (they can diverge via the FastPath router cache).
		providerName := actualModel.ProviderName
		if providerName == "" {
			providerName = provider.Name()
		}
		resolvedProvider = providerName

		// Set model resolution attributes
		attrs.SetModelResolution(
			resolveSpan,
			model,                 // requested
			providerName,          // provider
			actualModel.ModelName, // resolved
			"direct",              // strategy
			resolveDuration,       // durationMs
			true,                  // routerEnabled
			false,                 // fallbackAvailable
		)

		resolveSpan.SetStatus(codes.Ok, "model resolved")
		resolveSpan.End()
	} else {
		resolveSpan.End()
	}

	// Primary provider call
	norm := gw.EmbeddingsRequest{Model: model, Input: inputText}
	resp, err := gw.HandleEmbeddings(ctx, base.routerFor(ctx), norm)
	if err == nil {
		// Start response processing span
		_, respSpan := telemetry.StartResponseProcessingSpan(ctx)

		// Record success metrics
		latencyMs := time.Since(startTime).Milliseconds()

		// Get token count from response or estimate
		inputTokens := 0
		if resp.Usage != nil {
			inputTokens = resp.Usage.PromptTokens
		} else {
			inputTokens = len(inputText) / 4
			if inputTokens < 1 {
				inputTokens = 1
			}
		}

		// Calculate cost using the provider resolved above; only re-resolve as a
		// fallback (e.g. when the router was unavailable at resolution time).
		providerName := resolvedProvider
		if providerName == "" {
			providerName = base.getProviderForModel(ctx, model)
		}
		costDetails := metrics.CalculateCost(providerName, model, inputTokens, 0, 0)
		attrs.SetCostDetails(span, costDetails.EstimatedUSD, costDetails.ActualUSD, costDetails.SavingsUSD, costDetails.CarbonSavedGrams, costDetails.PricingModel)

		// Calculate embedding magnitude for meaningful metrics
		var magnitude float64
		for _, v := range resp.Embedding {
			magnitude += v * v
		}
		magnitudeRounded := float64(int(magnitude*1000)) / 1000 // Round to 3 decimals

		// Set response processing attributes
		respSpan.SetAttributes(
			attribute.String("llm.response.model", resp.Model),
			attribute.Int("llm.embeddings.dimension", len(resp.Embedding)),
			attribute.Float64("llm.embeddings.magnitude", magnitudeRounded),
			attribute.Int("llm.tokens.input", inputTokens),
		)

		// Set token usage on response span
		attrs.SetLLMTokens(respSpan, int64(inputTokens), 0, int64(inputTokens))

		// Add span event for response complete
		telemetry.AddSpanEvent(respSpan, attrs.EventResponseComplete,
			attribute.Int("response.dimension", len(resp.Embedding)),
			attribute.Bool("response.cached", false))

		respSpan.SetStatus(codes.Ok, "response processed")
		respSpan.End()

		// Set root span attributes
		span.SetAttributes(
			attribute.String("model.served", resp.Model),
			attribute.String("provider", providerName),
			attribute.Int("llm.embeddings.dimension", len(resp.Embedding)),
			attribute.Float64("llm.embeddings.magnitude", magnitudeRounded),
			attribute.Int64("latency_ms", latencyMs),
			attribute.Int("llm.tokens.input", inputTokens),
			attribute.Bool("fallback.triggered", false),
		)

		// Set root span business metrics
		attrs.SetRootSpanBusinessMetrics(span,
			costDetails.EstimatedUSD, 0, costDetails.EstimatedUSD, // cost input/output/total
			0,                            // cost savings
			0,                            // carbon grams
			costDetails.CarbonSavedGrams, // carbon saved
			int64(inputTokens),           // tokens input
			0,                            // tokens output (embeddings don't have output tokens)
			int64(inputTokens),           // tokens total
			latencyMs,                    // total latency
			latencyMs,                    // TTFT (same as latency for embeddings)
			latencyMs,                    // provider latency
		)

		// Set performance metrics
		latencyCategory := categorizeLatency(float64(latencyMs))
		attrs.SetPerformanceMetrics(span, float64(latencyMs), 0, 0, 0, 0, latencyCategory)

		// Set trace output (embedding summary, not raw floats)
		span.SetAttributes(
			attribute.String("llm.output.summary", fmt.Sprintf("embedding[%d] magnitude=%.3f", len(resp.Embedding), magnitudeRounded)),
		)

		span.SetStatus(codes.Ok, "success")

		// Record usage metrics to license monitor
		base.recordUsageMetrics(int64(inputTokens), 0, costDetails.EstimatedUSD, 0, false)

		// Log success with meaningful embedding summary
		logger.WithFields(
			"correlation_id", correlationID,
			"model", resp.Model,
			"user_id", userID,
			"api_key_hash", apiKeyHash,
			"dimensions", len(resp.Embedding),
			"magnitude", magnitudeRounded,
			"input_tokens", inputTokens,
			"cost_usd", costDetails.EstimatedUSD,
			"latency_ms", latencyMs,
			"input_preview", inputPreview,
		).Info("embeddings: success")

		// Build OpenAI-compatible response
		emb := make([]float32, len(resp.Embedding))
		for i, v := range resp.Embedding {
			emb[i] = float32(v)
		}

		return send(&gatewaypb.EmbeddingsResponse{
			Object: "list",
			Data: []*gatewaypb.EmbeddingData{
				{
					Object:    "embedding",
					Embedding: emb,
					Index:     0,
				},
			},
			Model: resp.Model,
			Id:    correlationID,
			Usage: &gatewaypb.EmbeddingsUsage{
				PromptTokens: int32(inputTokens),
				TotalTokens:  int32(inputTokens),
				Cost:         &costDetails.EstimatedUSD,
			},
		})
	}

	// Fallbacks
	plan := base.fallbackPlan(ctx)
	for _, step := range plan {
		if len(step.Aliases) == 0 {
			continue
		}
		if step.Strategy == "parallel" && len(step.Aliases) > 1 {
			tx, cancel := context.WithCancel(ctx)
			resCh := make(chan gw.EmbeddingsResponse, 1)
			errCh := make(chan error, len(step.Aliases))
			for _, alias := range step.Aliases {
				al := alias
				go func() {
					if rr, e := gw.HandleEmbeddings(tx, base.routerFor(ctx), gw.EmbeddingsRequest{Model: al, Input: inputText}); e == nil {
						select {
						case resCh <- rr:
						default:
						}
					} else {
						errCh <- e
					}
				}()
			}
			select {
			case rr := <-resCh:
				cancel()

				// Record fallback success
				latencyMs := time.Since(startTime).Milliseconds()
				span.SetAttributes(
					attribute.Bool("fallback.triggered", true),
					attribute.Int("llm.embeddings.dimension", len(rr.Embedding)),
					attribute.Int64("latency_ms", latencyMs),
				)
				span.SetStatus(codes.Ok, "fallback succeeded")

				// Calculate tokens for fallback
				fallbackTokens := len(inputText) / 4
				if fallbackTokens < 1 {
					fallbackTokens = 1
				}
				if rr.Usage != nil {
					fallbackTokens = rr.Usage.PromptTokens
				}

				// Calculate cost for fallback and record usage metrics
				fallbackProvider := base.getProviderForModel(ctx, rr.Model)
				fallbackCost := metrics.CalculateCost(fallbackProvider, rr.Model, fallbackTokens, 0, 0)
				base.recordUsageMetrics(int64(fallbackTokens), 0, fallbackCost.EstimatedUSD, 0, false)

				emb := make([]float32, len(rr.Embedding))
				for i, v := range rr.Embedding {
					emb[i] = float32(v)
				}
				return send(&gatewaypb.EmbeddingsResponse{
					Object: "list",
					Data: []*gatewaypb.EmbeddingData{
						{
							Object:    "embedding",
							Embedding: emb,
							Index:     0,
						},
					},
					Model: rr.Model,
					Id:    correlationID,
					Usage: &gatewaypb.EmbeddingsUsage{
						PromptTokens: int32(fallbackTokens),
						TotalTokens:  int32(fallbackTokens),
					},
				})
			case <-tx.Done():
				cancel()
			}
			continue
		}
		for _, alias := range step.Aliases {
			if alias == "" || alias == model {
				continue
			}
			if rr, e := gw.HandleEmbeddings(ctx, base.routerFor(ctx), gw.EmbeddingsRequest{Model: alias, Input: inputText}); e == nil {
				// Record fallback success
				latencyMs := time.Since(startTime).Milliseconds()
				span.SetAttributes(
					attribute.Bool("fallback.triggered", true),
					attribute.String("fallback.model", alias),
					attribute.Int("llm.embeddings.dimension", len(rr.Embedding)),
					attribute.Int64("latency_ms", latencyMs),
				)
				span.SetStatus(codes.Ok, "fallback succeeded")

				// Calculate tokens for fallback
				fallbackTokens := len(inputText) / 4
				if fallbackTokens < 1 {
					fallbackTokens = 1
				}
				if rr.Usage != nil {
					fallbackTokens = rr.Usage.PromptTokens
				}

				// Calculate cost for fallback and record usage metrics
				seqFallbackProvider := base.getProviderForModel(ctx, rr.Model)
				seqFallbackCost := metrics.CalculateCost(seqFallbackProvider, rr.Model, fallbackTokens, 0, 0)
				base.recordUsageMetrics(int64(fallbackTokens), 0, seqFallbackCost.EstimatedUSD, 0, false)

				emb := make([]float32, len(rr.Embedding))
				for i, v := range rr.Embedding {
					emb[i] = float32(v)
				}
				return send(&gatewaypb.EmbeddingsResponse{
					Object: "list",
					Data: []*gatewaypb.EmbeddingData{
						{
							Object:    "embedding",
							Embedding: emb,
							Index:     0,
						},
					},
					Model: rr.Model,
					Id:    correlationID,
					Usage: &gatewaypb.EmbeddingsUsage{
						PromptTokens: int32(fallbackTokens),
						TotalTokens:  int32(fallbackTokens),
					},
				})
			}
		}
	}

	// Record final error (err is guaranteed to be non-nil here)
	telemetry.RecordError(span, err)

	return err
}

// grpcRequestAdapter adapts gRPC ChatCompletionRequest to the cache.ChatRequest interface.
type grpcRequestAdapter struct {
	req *gatewaypb.ChatCompletionRequest
}

func (a *grpcRequestAdapter) GetModel() string {
	return a.req.GetModel()
}

func (a *grpcRequestAdapter) GetTemperature() float64 {
	if a.req.Sampling != nil {
		return float64(a.req.Sampling.GetTemperature())
	}
	return 0
}

func (a *grpcRequestAdapter) GetMaxTokens() int {
	if a.req.Sampling != nil {
		return int(a.req.Sampling.GetMaxTokens())
	}
	return 0
}

func (a *grpcRequestAdapter) GetTopP() float64 {
	if a.req.Sampling != nil {
		return float64(a.req.Sampling.GetTopP())
	}
	return 0
}

func (a *grpcRequestAdapter) GetStream() bool {
	if a.req.Stream != nil {
		return *a.req.Stream
	}
	return false
}

func (a *grpcRequestAdapter) GetMessages() []cache.Message {
	msgs := make([]cache.Message, len(a.req.Messages))
	for i := range a.req.Messages {
		msgs[i] = &grpcMessageAdapter{a.req.Messages[i]}
	}
	return msgs
}

// grpcMessageAdapter adapts gRPC Message to the cache.Message interface.
type grpcMessageAdapter struct {
	msg *gatewaypb.Message
}

func (m *grpcMessageAdapter) GetRole() string {
	return m.msg.Role.String()
}

func (m *grpcMessageAdapter) GetContent() string {
	// Concatenate all text content parts
	var content string
	for _, part := range m.msg.Content {
		if part.Type == "text" {
			if text := part.GetText(); text != "" {
				content += text
			}
		}
	}
	return content
}

// extractQueryTextFromProto extracts the text content from the last user message.
// This is used as the key for semantic cache lookups.
func extractQueryTextFromProto(req *gatewaypb.ChatCompletionRequest) string {
	// Find the last user message
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role == gatewaypb.Role_ROLE_USER {
			// Concatenate all text content parts
			var content string
			for _, part := range msg.Content {
				if part.Type == "text" {
					if text := part.GetText(); text != "" {
						content += text
					}
				}
			}
			return content
		}
	}
	return ""
}

// extractTextFromContentParts extracts text content from a slice of ContentParts.
// This is used to get the response text from cached responses.
func extractTextFromContentParts(parts []gw.ContentPart) string {
	var content string
	for _, part := range parts {
		if part.Type == "text" && part.Text != nil {
			content += *part.Text
		}
	}
	return content
}

func isValidCachedChatResponse(resp gw.ChatCompletionResponse) bool {
	if resp.Created.IsZero() || len(resp.Choices) == 0 {
		return false
	}
	for _, choice := range resp.Choices {
		if len(choice.Message.ToolCalls) > 0 {
			return true
		}
		if strings.TrimSpace(extractTextFromContentParts(choice.Message.Content)) != "" {
			return true
		}
	}
	return false
}

// Helper functions for enhanced telemetry

// getPerformancePercentiles returns current performance percentiles
func getPerformancePercentiles() (p50, p95, p99 float64, count int) {
	// For now, return zeros - would integrate with metrics package
	return 0, 0, 0, 0
}

// categorizeLatency categorizes latency into performance buckets
func categorizeLatency(latencyMs float64) string {
	switch {
	case latencyMs < 100:
		return "excellent"
	case latencyMs < 500:
		return "good"
	case latencyMs < 2000:
		return "acceptable"
	default:
		return "slow"
	}
}

// classifyBusinessMetrics classifies business metrics from request and response
func classifyBusinessMetrics(messages []gw.Message, resp gw.ChatCompletionResponse) struct {
	UseCase      string
	Domain       string
	QueryType    string
	QualityScore float64
} {
	// Extract message text
	messageTexts := make([]string, 0, len(messages))
	for _, msg := range messages {
		for _, content := range msg.Content {
			if content.Type == "text" {
				messageTexts = append(messageTexts, *content.Text)
			}
		}
	}

	combinedText := strings.ToLower(strings.Join(messageTexts, " "))
	responseLength := 0
	if len(resp.Choices) > 0 {
		for _, content := range resp.Choices[0].Message.Content {
			if content.Type == "text" {
				responseLength += len(*content.Text)
			}
		}
	}

	// Simple classification
	useCase := "chat"
	if strings.HasSuffix(combinedText, "?") {
		if len(combinedText) < 100 {
			useCase = "qa_simple"
		} else {
			useCase = "qa_complex"
		}
	}

	domain := "general"
	if strings.Contains(combinedText, "earth") || strings.Contains(combinedText, "radius") || strings.Contains(combinedText, "geography") {
		domain = "geography"
	} else if strings.Contains(combinedText, "code") || strings.Contains(combinedText, "function") {
		domain = "code"
	}

	queryType := "factual"
	if strings.Contains(combinedText, "create") || strings.Contains(combinedText, "write") {
		queryType = "creative"
	}

	qualityScore := 0.8
	if responseLength > 100 && responseLength < 1000 {
		qualityScore = 0.9
	}

	return struct {
		UseCase      string
		Domain       string
		QueryType    string
		QualityScore float64
	}{
		UseCase:      useCase,
		Domain:       domain,
		QueryType:    queryType,
		QualityScore: qualityScore,
	}
}

// rtconfigFromCtx pulls the per-tenant runtime config service out of
// the request or server context. Returns nil if neither carries one
// (tests, CE single-binary). Callers should fail-open in that case so
// the gate degrades to "feature allowed" rather than blocking traffic.
func rtconfigFromCtx(ctx context.Context, base *Server) *rtconfig.Service {
	if svc, ok := ctx.Value(contextkeys.RuntimeConfigService).(*rtconfig.Service); ok && svc != nil {
		return svc
	}
	if base != nil && base.ctx != nil {
		if svc, ok := base.ctx.Value(contextkeys.RuntimeConfigService).(*rtconfig.Service); ok && svc != nil {
			return svc
		}
	}
	return nil
}
