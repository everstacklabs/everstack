package attributes

// Attribute name constants - single source of truth for all span attributes
// This prevents typos and provides IDE autocomplete for attribute names

const (
	// Request attributes - basic request metadata
	RequestStream       = "request.stream"
	RequestMessageCount = "request.message_count"
	RequestModel        = "request.model"
	RequestUserID       = "request.user_id"
	RequestAPIKeyHash   = "request.api_key_hash"
	RequestTenantID     = "request.tenant_id"

	// Request message structure attributes
	RequestMessageRoles      = "request.message_roles"       // JSON array of role strings
	RequestMessageSizes      = "request.message_sizes"       // JSON array of message sizes in bytes
	RequestHasSystemPrompt   = "request.has_system_prompt"   // bool
	RequestHasImages         = "request.has_images"          // bool
	RequestHasToolCalls      = "request.has_tool_calls"      // bool
	RequestContentTypes      = "request.content_types"       // JSON array of content type arrays per message
	RequestTotalContentChars = "request.total_content_chars" // Total character count across all messages

	// Request sampling parameters
	RequestTemperature      = "request.temperature"
	RequestTopP             = "request.top_p"
	RequestMaxTokens        = "request.max_tokens"
	RequestStop             = "request.stop" // JSON array
	RequestFrequencyPenalty = "request.frequency_penalty"
	RequestPresencePenalty  = "request.presence_penalty"

	// Normalization metadata
	NormalizationDurationMs    = "normalization.duration_ms"
	NormalizationConfigApplied = "normalization.config_applied" // bool - whether config overrides were applied

	// Model resolution attributes
	ModelRequested              = "model.requested"
	ModelProvider               = "model.provider"
	ModelResolved               = "model.resolved"
	ResolutionStrategy          = "resolution.strategy" // "direct", "alias", "fallback"
	ResolutionDurationMs        = "resolution.duration_ms"
	ResolutionRouterEnabled     = "resolution.router_enabled"     // bool
	ResolutionFallbackAvailable = "resolution.fallback_available" // bool

	// HTTP request attributes
	HTTPMethod             = "http.method"
	HTTPURL                = "http.url"
	HTTPRequestBodySize    = "http.request.body_size"
	HTTPRequestContentType = "http.request.content_type"

	// HTTP response attributes
	HTTPStatusCode          = "http.status_code"
	HTTPResponseBodySize    = "http.response.body_size"
	HTTPResponseContentType = "http.response.content_type"
	HTTPLatencyMs           = "http.latency_ms"

	// Rate limit attributes
	RateLimitRequests          = "ratelimit.limit_requests"
	RateLimitTokens            = "ratelimit.limit_tokens"
	RateLimitRemainingRequests = "ratelimit.remaining_requests"
	RateLimitRemainingTokens   = "ratelimit.remaining_tokens"
	RateLimitResetRequests     = "ratelimit.reset_requests"
	RateLimitResetTokens       = "ratelimit.reset_tokens"

	// LLM request attributes
	LLMOperation                     = "llm.operation" // e.g., "chat", "embeddings"
	LLMRequestModel                  = "llm.request.model"
	LLMRequestMessageCount           = "llm.request.message_count"
	LLMRequestStream                 = "llm.request.stream"
	LLMRequestMessages               = "llm.request.messages"             // JSON payload (full, not truncated)
	LLMRequestModelParameters        = "llm.request.model_parameters"     // JSON parameters
	LLMRequestSystemPromptLength     = "llm.request.system_prompt_length" // chars
	LLMRequestUserMessagesCount      = "llm.request.user_messages_count"
	LLMRequestAssistantMessagesCount = "llm.request.assistant_messages_count"
	LLMRequestTotalContentLength     = "llm.request.total_content_length" // chars

	// LLM response attributes
	LLMResponseModel         = "llm.response.model"
	LLMResponseID            = "llm.response.id"
	LLMResponseChoices       = "llm.response.choices" // JSON payload (full, not truncated)
	LLMResponseChoiceCount   = "llm.response.choice_count"
	LLMResponseContentLength = "llm.response.content_length" // chars of generated text
	LLMResponseFinishReason  = "llm.response.finish_reason"
	LLMResponseCreatedAt     = "llm.response.created_at"

	// LLM token usage attributes
	LLMTokensInput    = "llm.tokens.input"
	LLMTokensOutput   = "llm.tokens.output"
	LLMTokensTotal    = "llm.tokens.total"
	AgentTokensInput  = "agent.tokens.input"
	AgentTokensOutput = "agent.tokens.output"
	AgentTokensTotal  = "agent.tokens.total"

	// LLM token breakdown attributes (detailed per-type breakdown)
	LLMTokensCached            = "llm.tokens.cached"             // Aggregate cache_read + cache_write (legacy; kept for back-compat)
	LLMTokensCacheRead         = "llm.tokens.cache_read"         // Cache hits — re-using a prior request's cached prefix
	LLMTokensCacheWrite        = "llm.tokens.cache_write"        // Cache writes — tokens written into cache (Anthropic only)
	LLMTokensReasoning         = "llm.tokens.reasoning"          // Reasoning tokens (o1/o3 models, Gemini Thinking)
	LLMTokensAudio             = "llm.tokens.audio"              // Audio tokens (multimodal)
	LLMTokensImage             = "llm.tokens.image"              // Image tokens (vision models)
	LLMTokensText              = "llm.tokens.text"               // Text-only tokens (when audio/image separate)
	LLMTokensPromptDetails     = "llm.tokens.prompt_details"     // JSON breakdown of prompt tokens
	LLMTokensCompletionDetails = "llm.tokens.completion_details" // JSON breakdown of completion tokens
	LLMTokensPerMessage        = "llm.tokens.per_message"        // JSON array of per-message token counts

	// LLM cost attributes
	LLMCostInput    = "llm.cost.input"    // USD
	LLMCostOutput   = "llm.cost.output"   // USD
	LLMCostTotal    = "llm.cost.total"    // USD
	LLMCostCurrency = "llm.cost.currency" // "USD"

	// LLM streaming attributes
	LLMStreamChunkIndex          = "llm.stream.chunk_index" // Index of individual chunk
	LLMStreamChunkCount          = "llm.stream.chunk_count"
	LLMStreamFirstChunkLatencyMs = "llm.stream.first_chunk_latency_ms"
	LLMStreamTotalLatencyMs      = "llm.stream.total_latency_ms"
	LLMStreamTotalContentLength  = "llm.stream.total_content_length" // accumulated chars
	LLMStreamAverageChunkSize    = "llm.stream.average_chunk_size"   // chars
	LLMStreamTimeToFirstTokenMs  = "llm.stream.time_to_first_token_ms"

	// Response processing attributes
	ResponseModel                = "response.model"
	ResponseID                   = "response.id"
	ResponseChoiceCount          = "response.choice_count"
	ResponseProcessingDurationMs = "response.processing_duration_ms"
	ResponseTotalContentLength   = "response.total_content_length" // chars across all choices
	ResponseHasMultipleChoices   = "response.has_multiple_choices" // bool
	ResponseFinishReasons        = "response.finish_reasons"       // JSON array if multiple choices
	ResponsePromptTokens         = "response.prompt_tokens"
	ResponseCompletionTokens     = "response.completion_tokens"
	ResponseTotalTokens          = "response.total_tokens"

	// General span attributes
	LatencyMs  = "latency_ms"
	Provider   = "provider"
	TenantID   = "tenant.id"    // Tenant identifier (general use)
	UserID     = "user_id"      // User identifier (general use)
	APIKeyHash = "api_key_hash" // Hashed API key for security

	// Public model-metrics identity. Serving provider remains distinct from
	// publisher/canonical identity so the same model can aggregate across
	// direct, cloud-platform, and meta-provider routes.
	TrafficKind      = "everstack.traffic.kind" // customer | internal
	ModelPublisher   = "model.publisher"
	CanonicalModelID = "model.canonical_id"

	// Trace-level attributes (set on root span)
	TraceInput     = "trace.input"      // JSON payload of input (full, not truncated)
	TraceOutput    = "trace.output"     // JSON payload of output (full, not truncated)
	TraceSessionID = "trace.session_id" // Session identifier
	TraceThreadID  = "trace.thread_id"  // Conversational thread, distinct from session
	TraceUserID    = "trace.user_id"    // User identifier (trace level)
	TraceName      = "trace.name"       // Trace name/description
	TraceTags      = "trace.tags"       // JSON array of tags
	TraceMetadata  = "trace.metadata"   // JSON object of metadata

	// Correlation and linking
	CorrelationID = "correlation_id" // Request correlation ID for log-to-trace linking

	// Fallback attributes
	FallbackAttempt   = "fallback.attempt"   // Fallback attempt number
	FallbackReason    = "fallback.reason"    // Reason for fallback
	FallbackTriggered = "fallback.triggered" // Whether fallback was triggered (bool)

	// Observation metadata (Langfuse/Portkey compatibility)
	ObservationType  = "observation.type"  // Type: see telemetry.ObservationType taxonomy
	ObservationLevel = "observation.level" // Level: DEFAULT, DEBUG, ERROR

	// Observation provenance (traces-module-replan section 4.6 / 4.7)
	ObservationPurpose = "observation.purpose"    // Purpose flag, e.g. "scorer" (excluded from host rollups)
	ObservationOrigin  = "observation.origin"     // Origin: prod | playground | eval | replay | sdk
	ExecIndex          = "observation.exec_index" // Execution ordering when timestamps collide

	// Execution roots and trace composition (traces-module-replan section 4.7)
	RootType     = "run.root_type"      // agent | workflow | pipeline | harness | eval | playground
	RunID        = "run.id"             // Stable id of the execution root
	ParentRunRef = "run.parent_run_ref" // Link to the parent run for nested roots

	// Workflow/execution metadata
	StepNumber   = "step.number"   // Execution step number for ordering
	NodeName     = "node.name"     // Workflow node name
	NodeType     = "node.type"     // Workflow node type (Studio): provider, cache, ifElse, ...
	NodeID       = "node.id"       // Workflow node id, for canvas run-replay mapping
	WorkflowID   = "workflow.id"   // Workflow definition id (span attribute)
	WorkflowName = "workflow.name" // Workflow name (span attribute)

	// Memory / vector store attributes (M1-T7)
	VectorBackend       = "vector.backend"        // pgvector | qdrant | pinecone | weaviate
	VectorOperation     = "vector.operation"      // query | store | add_documents
	VectorCollection    = "vector.collection_id"  // Target collection
	VectorTopK          = "vector.top_k"          // Requested neighbours
	VectorResultCount   = "vector.result_count"   // Rows returned / written
	EmbeddingModel      = "embedding.model"       // Embedding model id
	EmbeddingDimension  = "embedding.dimension"   // Vector dimensionality
	EmbeddingInputCount = "embedding.input_count" // Number of texts embedded

	// Agent memory attributes (M1-T6)
	MemoryOperation   = "memory.operation"    // retrieve | extract | consolidate
	MemoryResultCount = "memory.result_count" // Memories retrieved / written

	// Harness / ADK + integration attributes (M1-T8 / M1-T9)
	HarnessPackages     = "harness.packages"     // Count of extra pip packages installed
	IntegrationProvider = "integration.provider" // github | gitlab | linear | jira ...

	// Browser automation attributes (M1-T4)
	BrowserAction   = "browser.action"   // navigate | click | type | screenshot | ...
	BrowserURL      = "browser.url"      // Target URL (navigate)
	BrowserSelector = "browser.selector" // Element selector / index

	// MCP / A2A attributes (M1-T10)
	MCPServerID = "mcp.server_id" // MCP server id
	MCPToolName = "mcp.tool_name" // MCP tool invoked
	A2ATarget   = "a2a.target"    // Remote agent name or endpoint host

	// Scorer / eval attributes (M3)
	ScorerName            = "scorer.name"             // Scorer identifier
	ScoreCount            = "scorer.score_count"      // Number of scores produced
	ScoringState          = "scoring.state"           // Compact status summary (e.g. "2 done, 1 failed")
	ScoringIdempotencyKey = "scoring.idempotency_key" // Per-scorer idempotency key for dedup/retry

	// Guardrail span-event attributes (D7) — keys match the frontend parser in
	// apps/admin/src/utils/guardrail-events.ts so checks render as a safety summary.
	GuardrailResult     = "result"         // pass | block
	GuardrailRule       = "guardrail.rule" // category/rule that fired
	GuardrailViolations = "violations"     // detail of what was flagged

	// Provider-specific metadata (enhanced observability)
	ProviderAPIVersion   = "provider.api_version"    // Provider API version
	ProviderRegion       = "provider.region"         // Provider region/datacenter
	ProviderEndpoint     = "provider.endpoint"       // Provider endpoint URL
	ProviderAPIKeyID     = "provider.api_key_id"     // Upstream provider API key ID (uuid) that served the call
	ProviderAPIKeyName   = "provider.api_key_name"   // Human label of the upstream provider API key
	ProviderAPIKeySource = "provider.api_key_source" // Provenance of the upstream provider API key

	// Timing breakdowns (enhanced observability)
	TimingNormalization   = "timing.normalization_ms"    // Time spent normalizing request
	TimingModelResolution = "timing.model_resolution_ms" // Time spent resolving model
	TimingProviderCall    = "timing.provider_call_ms"    // Time spent in provider API call
	TimingResponseProcess = "timing.response_process_ms" // Time spent processing response

	// Token efficiency metrics (enhanced observability)
	TokensPerSecond = "llm.tokens_per_second" // Throughput in tokens/second
	CostPerToken    = "llm.cost_per_token"    // Cost efficiency in USD per token

	// Streaming enhancements (enhanced observability)
	StreamInterChunkLatencyMs = "llm.stream.inter_chunk_latency_ms" // Average time between chunks
	StreamChunkSizes          = "llm.stream.chunk_sizes"            // JSON array of chunk sizes
	StreamMinChunkSize        = "llm.stream.min_chunk_size"         // Minimum chunk size in chars
	StreamMaxChunkSize        = "llm.stream.max_chunk_size"         // Maximum chunk size in chars

	// Error details (enhanced observability)
	ErrorType      = "error.type"      // Error type/category
	ErrorRetryable = "error.retryable" // Whether error is retryable (bool)
	ErrorProvider  = "error.provider"  // Provider that generated the error

	// Cache/optimization hints (enhanced observability)
	CacheHit = "cache.hit" // Whether request was served from cache (bool)
	CacheKey = "cache.key" // Cache key used

	// Cache operation attributes (granular cache tracing)
	CacheType      = "cache.type"      // "exact", "semantic", "onnx", "auth", "router"
	CacheOperation = "cache.operation" // "lookup", "store", "warm"
	CacheKeyHash   = "cache.key_hash"  // xxhash of request (uint64 as string)
	CacheTTL       = "cache.ttl_seconds"
	CacheEntryAge  = "cache.entry_age_ms"
	CacheSize      = "cache.size_bytes"
	CacheLatencyMs = "cache.latency_ms"

	// Semantic cache attributes (embedding-based similarity)
	SemanticSimilarityScore     = "cache.semantic.similarity_score"
	SemanticThreshold           = "cache.semantic.threshold"
	SemanticQueryLength         = "cache.semantic.query_length"
	SemanticEmbeddingModel      = "cache.semantic.embedding_model"
	SemanticEmbeddingDimensions = "cache.semantic.embedding_dimensions"
	SemanticEmbeddingLatencyMs  = "cache.semantic.embedding_latency_ms"

	// Vector search attributes (Redis vector operations)
	VectorSearchIndexName    = "cache.vector.index_name"
	VectorSearchTopK         = "cache.vector.top_k"
	VectorSearchResultCount  = "cache.vector.result_count"
	VectorSearchLatencyMs    = "cache.vector.search_latency_ms"
	VectorSearchDistance     = "cache.vector.distance"      // Distance metric used
	VectorSearchBestScore    = "cache.vector.best_score"    // Best similarity score
	VectorSearchQueryVector  = "cache.vector.query_vector"  // Query vector (full, not truncated)
	VectorSearchResultHashes = "cache.vector.result_hashes" // Result key hashes (JSON array)

	// MinHash attributes (lexical similarity)
	MinHashSignatureSize     = "cache.minhash.signature_size"
	MinHashBandCount         = "cache.minhash.band_count"
	MinHashJaccardSimilarity = "cache.minhash.jaccard_similarity"
	MinHashShingleSize       = "cache.minhash.shingle_size"
	MinHashCandidateCount    = "cache.minhash.candidate_count" // Number of LSH candidates

	// ONNX cache attributes (deep semantic understanding)
	ONNXModelPath           = "cache.onnx.model_path"
	ONNXInferenceLatencyMs  = "cache.onnx.inference_latency_ms"
	ONNXEmbeddingDimensions = "cache.onnx.embedding_dimensions"
	ONNXTokenizerType       = "cache.onnx.tokenizer_type"
	ONNXInputTokenCount     = "cache.onnx.input_token_count"

	// Auth cache attributes (Bloom filter-based auth)
	AuthCacheBloomFilterSize = "cache.auth.bloom_filter_size"
	AuthCacheHashCount       = "cache.auth.hash_count"
	AuthCacheFalsePositive   = "cache.auth.false_positive" // Whether this was a false positive
	AuthCacheKeyHash         = "cache.auth.key_hash"       // Hash of API key
	AuthCacheValidated       = "cache.auth.validated"      // Whether key was validated

	// Router cache attributes (model resolution)
	RouterCacheWarmed     = "cache.router.warmed"
	RouterCacheEntryCount = "cache.router.entry_count"
	RouterCacheModelName  = "cache.router.model_name"
	RouterCacheProvider   = "cache.router.provider"
	RouterCacheResolved   = "cache.router.resolved" // Whether model was resolved

	// Cost & Carbon attributes (detailed cost tracking)
	CostEstimatedUSD = "cost.estimated_usd" // Estimated cost in USD
	CostActualUSD    = "cost.actual_usd"    // Actual cost from provider in USD
	CostSavingsUSD   = "cost.savings_usd"   // Cost savings from cache/optimization in USD
	CostPricingModel = "cost.pricing_model" // Pricing model: "pay_per_token", "subscription", etc.
	CarbonSavedGrams = "carbon.saved_grams" // Estimated carbon saved in grams CO2
	CostInputTokens  = "cost.input_tokens"  // Token count used for cost calculation
	CostOutputTokens = "cost.output_tokens" // Token count used for cost calculation

	// Performance Percentiles (detailed performance metrics)
	PerformanceLatencyP50Ms           = "performance.latency_p50_ms"            // P50 latency in milliseconds
	PerformanceLatencyP95Ms           = "performance.latency_p95_ms"            // P95 latency in milliseconds
	PerformanceLatencyP99Ms           = "performance.latency_p99_ms"            // P99 latency in milliseconds
	PerformanceTTFBMs                 = "performance.ttfb_ms"                   // Time to first byte in milliseconds
	PerformanceThroughputTokensPerSec = "performance.throughput_tokens_per_sec" // Tokens per second throughput
	PerformanceLatencyCategory        = "performance.latency_category"          // Category: "excellent", "good", "acceptable", "slow"
	PerformanceThroughputCategory     = "performance.throughput_category"       // Category: "high", "medium", "low"

	// Business Metrics (business intelligence attributes)
	BusinessUseCase              = "business.use_case"               // Use case: "qa_simple", "chat", "summarization", etc.
	BusinessDomain               = "business.domain"                 // Domain: "geography", "science", "code", etc.
	BusinessQueryType            = "business.query_type"             // Query type: "factual", "creative", "analytical"
	BusinessResponseQualityScore = "business.response_quality_score" // Quality score 0.0-1.0
	BusinessQueryComplexity      = "business.query_complexity"       // Complexity: "simple", "moderate", "complex"
	BusinessDomainConfidence     = "business.domain_confidence"      // Confidence: "high", "medium", "low"

	// User Context (user and quota information)
	UserTier              = "user.tier"                // User tier: "free", "premium", "enterprise"
	UserSessionID         = "user.session_id"          // User session identifier
	UserRequestCountToday = "user.request_count_today" // Number of requests made today
	UserQuotaRemaining    = "user.quota_remaining"     // Remaining quota for user
	UserExperienceScore   = "user.experience_score"    // User experience score 0.0-1.0
	UserSatisfactionScore = "user.satisfaction_score"  // User satisfaction score 0.0-1.0

	// Security Context (security and rate limiting)
	SecurityAPIKeyHash         = "security.api_key_hash"         // Hashed API key
	SecurityRateLimitRemaining = "security.rate_limit_remaining" // Remaining rate limit
	SecurityRateLimitResetAt   = "security.rate_limit_reset_at"  // Rate limit reset timestamp

	// Cache Advanced Metrics (detailed cache analytics)
	CacheStorageBackend       = "cache.storage_backend"       // Storage backend: "redis", "memory", "disk"
	CacheCompression          = "cache.compression"           // Compression: "gzip", "lz4", "none"
	CacheCompressionRatio     = "cache.compression_ratio"     // Compression ratio (0.0-1.0)
	CacheHitRateLastHour      = "cache.hit_rate_last_hour"    // Cache hit rate in last hour (0.0-1.0)
	CacheRetrievalMs          = "cache.retrieval_ms"          // Cache retrieval time in milliseconds
	CacheSpeedupFactor        = "cache.speedup_factor"        // Speedup factor vs non-cached (e.g., 484)
	CacheEfficiencyPercentage = "cache.efficiency_percentage" // Cache efficiency percentage (0-100)
	CacheCachedFromRequest    = "cache.cached_from_request"   // Original request ID that populated cache
	CacheAgeSeconds           = "cache.age_seconds"           // Age of cached entry in seconds

	// Request Details (full request context)
	RequestID             = "request.id"               // Request identifier (correlation ID)
	RequestInput          = "request.input"            // Full request input (JSON)
	RequestInputTokens    = "request.input_tokens"     // Estimated input tokens
	RequestInputSizeBytes = "request.input_size_bytes" // Input size in bytes
	RequestClientIP       = "request.client_ip"        // Client IP address
	RequestUserAgent      = "request.user_agent"       // User agent string

	// Response Details (full response context)
	ResponseOutput            = "response.output"              // Full response output (JSON)
	ResponseOutputTokens      = "response.output_tokens"       // Output tokens
	ResponseOutputSizeBytes   = "response.output_size_bytes"   // Output size in bytes
	ResponseFinishReason      = "response.finish_reason"       // Finish reason: "complete", "length", "stop"
	ResponseModelUsed         = "response.model_used"          // Actual model used for response
	ResponseCached            = "response.cached"              // Whether response was from cache
	ResponseCachedFromRequest = "response.cached_from_request" // Original request that generated cached response

	// Model Details (comprehensive model information)
	ModelFamily          = "model.family"            // Model family: "gpt", "claude", "command", etc.
	ModelVersion         = "model.version"           // Model version
	ModelCapabilities    = "model.capabilities"      // Capabilities: "chat,completion,embeddings"
	ModelContextWindow   = "model.context_window"    // Context window size
	ModelMaxOutputTokens = "model.max_output_tokens" // Maximum output tokens

	// Resolution Details (detailed model resolution info)
	ResolutionInput             = "resolution.input"               // Resolution input (JSON)
	ResolutionOutput            = "resolution.output"              // Resolution output (JSON)
	ResolutionFallbackModels    = "resolution.fallback_models"     // Available fallback models (comma-separated)
	ResolutionRouterVersion     = "resolution.router_version"      // Router version
	ResolutionLoadBalancing     = "resolution.load_balancing"      // Load balancing: "enabled", "disabled"
	ResolutionHealthCheckPassed = "resolution.health_check_passed" // Health check passed (bool)
	ResolutionModelAvailable    = "resolution.model_available"     // Model available (bool)
	ResolutionQuotaCheck        = "resolution.quota_check"         // Quota check result: "passed", "failed"

	// Provider Details (provider health and performance)
	ProviderHealthStatus     = "provider.health_status"      // Health status: "healthy", "degraded", "unhealthy"
	ProviderCurrentLatencyMs = "provider.current_latency_ms" // Current provider latency in milliseconds
	ProviderErrorRate        = "provider.error_rate"         // Provider error rate (0.0-1.0)

	// Normalization Details (detailed normalization tracking)
	NormalizationInput           = "normalization.input"           // Normalization input (JSON)
	NormalizationOutput          = "normalization.output"          // Normalization output (JSON)
	NormalizationConfigVersion   = "normalization.config_version"  // Config version
	NormalizationRulesApplied    = "normalization.rules_applied"   // Rules applied (comma-separated)
	NormalizationTransformations = "normalization.transformations" // Number of transformations
	ValidationPassed             = "validation.passed"             // Validation passed (bool)
	ValidationErrors             = "validation.errors"             // Validation errors (JSON array)
	ValidationWarnings           = "validation.warnings"           // Validation warnings (JSON array)
	SchemaVersion                = "schema.version"                // Schema version
	SchemaValidated              = "schema.validated"              // Schema validated (bool)

	// Storage Details (storage operation details)
	StorageOperationType           = "storage.operation_type"            // Operation: "write", "read", "delete"
	StorageSuccess                 = "storage.success"                   // Operation success (bool)
	StorageRetryCount              = "storage.retry_count"               // Number of retries
	StorageConnectionPoolSize      = "storage.connection_pool_size"      // Connection pool size
	StorageConnectionWaitMs        = "storage.connection_wait_ms"        // Connection wait time in milliseconds
	NetworkBytesSent               = "network.bytes_sent"                // Bytes sent over network
	NetworkProtocol                = "network.protocol"                  // Network protocol: "tcp", "http", "grpc"
	PerformanceWriteThroughputMbps = "performance.write_throughput_mbps" // Write throughput in Mbps

	// Additional Cache Lookup Attributes (granular cache tracking)
	CacheEnabled            = "cache.enabled"              // Whether cache is enabled (bool)
	CacheLookupDurationMs   = "cache.lookup_duration_ms"   // Total lookup time across all caches
	CacheExactChecked       = "cache.exact.checked"        // Whether exact cache was checked (bool)
	CacheExactHit           = "cache.exact.hit"            // Whether exact cache hit (bool)
	CacheExactDurationMs    = "cache.exact.duration_ms"    // Exact cache lookup time
	CacheSemanticChecked    = "cache.semantic.checked"     // Whether semantic cache was checked (bool)
	CacheSemanticHit        = "cache.semantic.hit"         // Whether semantic cache hit (bool)
	CacheSemanticDurationMs = "cache.semantic.duration_ms" // Semantic cache lookup time
	CacheQueryTextLength    = "cache.query_text_length"    // Length of query text used for cache lookup
	CacheCandidatesFound    = "cache.candidates_found"     // Number of LSH/vector candidates found

	// Semantic Cache Embedding Attributes
	SemanticInputTokens     = "cache.semantic.input_tokens"      // Input tokens for embedding
	SemanticInputTextLength = "cache.semantic.input_text_length" // Input text length for embedding

	// Semantic Cache Search Attributes
	SemanticSearchBackend         = "cache.semantic.search_backend"     // Search backend: "redis", "memory"
	SemanticSearchDurationMs      = "cache.semantic.search_duration_ms" // Vector search time
	SemanticCandidatesReturned    = "cache.semantic.candidates_returned"
	SemanticBestScore             = "cache.semantic.best_score"        // Best similarity score
	SemanticThresholdMet          = "cache.semantic.threshold_met"     // Whether threshold was met (bool)
	SemanticCacheTotalEntries     = "cache.semantic.total_entries"     // Total entries in semantic cache
	SemanticCacheMemoryHits       = "cache.semantic.memory_hits"       // Hits from memory fallback
	SemanticCacheEmbeddingsCached = "cache.semantic.embeddings_cached" // Whether embedding was cached

	// Additional Streaming Attributes
	LLMStreamAvgChunkLatencyMs = "llm.stream.avg_chunk_latency_ms" // Average time between chunks
	LLMStreamTotalDurationMs   = "llm.stream.total_duration_ms"    // Total streaming duration
	LLMStreamTokensStreamed    = "llm.stream.tokens_streamed"      // Total tokens streamed
	LLMStreamBytesStreamed     = "llm.stream.bytes_streamed"       // Total bytes streamed
	LLMStreamChunksReceived    = "llm.stream.chunks_received"      // Total chunks received
	LLMStreamFirstTokenMs      = "llm.stream.first_token_ms"       // Time to first token (TTFT)
	LLMStreamLastChunkMs       = "llm.stream.last_chunk_ms"        // Time of last chunk relative to start
	LLMStreamInterruptedReason = "llm.stream.interrupted_reason"   // Reason if stream was interrupted

	// Additional Response Attributes
	ResponseHasToolCalls    = "response.has_tool_calls"    // Whether response contains tool calls (bool)
	ResponseHasFunctionCall = "response.has_function_call" // Whether response contains function call (bool)
	ResponseOutputRaw       = "response.output_raw"        // Raw output JSON

	// Additional Fallback Attributes
	FallbackModel        = "fallback.model"         // Model used in fallback
	FallbackProvider     = "fallback.provider"      // Provider used in fallback
	FallbackStrategy     = "fallback.strategy"      // Fallback strategy: "priority", "parallel", "round_robin"
	FallbackTimeoutMs    = "fallback.timeout_ms"    // Timeout configured for fallback
	FallbackBackoffMs    = "fallback.backoff_ms"    // Backoff configured for fallback
	FallbackSuccess      = "fallback.success"       // Whether fallback succeeded (bool)
	FallbackError        = "fallback.error"         // Error message if fallback failed
	FallbackLatencyMs    = "fallback.latency_ms"    // Latency of fallback attempt
	FallbackAttempts     = "fallback.attempts"      // Total number of fallback attempts
	FallbackExhausted    = "fallback.exhausted"     // Whether all fallbacks were exhausted (bool)
	FallbackLastError    = "fallback.last_error"    // Last error before fallbacks exhausted
	FallbackPrimaryModel = "fallback.primary_model" // Original primary model that failed

	// Key Rotation Attributes
	KeyRotationAttempt       = "key_rotation.attempt"        // Key rotation attempt number
	KeyRotationReason        = "key_rotation.reason"         // Reason for key rotation
	KeyRotationSuccess       = "key_rotation.success"        // Whether rotation succeeded (bool)
	KeyRotationKeysAvailable = "key_rotation.keys_available" // Number of keys available
	KeyRotationDurationMs    = "key_rotation.duration_ms"    // Key rotation duration
	KeyRotationKeysTried     = "key_rotation.keys_tried"     // Number of keys tried

	// Business Metrics (root span level)
	CostInputUSD      = "cost.input_usd"      // Input token cost in USD
	CostOutputUSD     = "cost.output_usd"     // Output token cost in USD
	CarbonGrams       = "carbon.grams"        // Carbon footprint estimate in grams
	TokensInput       = "tokens.input"        // Total input tokens (alias for easy access)
	TokensOutput      = "tokens.output"       // Total output tokens (alias for easy access)
	TokensTotal       = "tokens.total"        // Total tokens (alias for easy access)
	LatencyTotalMs    = "latency.total_ms"    // Total latency
	LatencyTTFTMs     = "latency.ttft_ms"     // Time to first token
	LatencyProviderMs = "latency.provider_ms" // Provider latency only

	// Trace-level Additional Attributes
	TraceEnvironment = "trace.environment" // Deployment environment
	TraceRelease     = "trace.release"     // App version/release

	// Model Served Attribute
	ModelServed = "model.served" // Actual model that served the request

	// Resolution Additional Attributes
	ResolutionAliasMatched         = "resolution.alias_matched"          // Whether alias was matched (bool)
	ResolutionLoadBalancerStrategy = "resolution.load_balancer_strategy" // LB strategy used

	// Span Event Names (for detailed timeline tracking)
	// These are used with span.AddEvent() to mark significant points in time
	EventCacheLookupStart            = "cache.lookup.start"
	EventCacheLookupHit              = "cache.lookup.hit"
	EventCacheLookupMiss             = "cache.lookup.miss"
	EventCacheExactLookup            = "cache.exact.lookup"
	EventCacheSemanticLookup         = "cache.semantic.lookup"
	EventCacheStoreStart             = "cache.store.start"
	EventCacheStoreComplete          = "cache.store.complete"
	EventCacheCompressionComplete    = "cache.compression.complete"
	EventNormalizationStart          = "normalization.start"
	EventNormalizationComplete       = "normalization.complete"
	EventValidationSchemaCheck       = "validation.schema_check"
	EventTransformationApplyDefaults = "transformation.apply_defaults"
	EventResolutionStart             = "resolution.start"
	EventResolutionLookup            = "resolution.lookup"
	EventResolutionComplete          = "resolution.complete"
	EventResponseComplete            = "response.complete"
	EventResponseProcessingStart     = "response.processing.start"
	EventProviderCallStart           = "provider.call.start"
	EventProviderCallComplete        = "provider.call.complete"
	EventHTTPRequestSent             = "http.request.sent"
	EventHTTPResponseReceived        = "http.response.received"

	// Streaming Events
	EventStreamStart      = "stream.start"
	EventStreamFirstChunk = "stream.first_chunk"
	EventStreamChunk      = "stream.chunk"
	EventStreamComplete   = "stream.complete"

	// Fallback Events
	EventFallbackStarted   = "fallback.started"
	EventFallbackAttempt   = "fallback.attempt"
	EventFallbackSuccess   = "fallback.success"
	EventFallbackExhausted = "fallback.exhausted"

	// Key Rotation Events
	EventKeyRotationStart    = "key_rotation.start"
	EventKeyRotationComplete = "key_rotation.complete"

	// Embedding Events (for semantic cache)
	EventEmbeddingGenerationStart    = "embedding.generation.start"
	EventEmbeddingGenerationComplete = "embedding.generation.complete"

	// Vector Search Events
	EventVectorSearchStart    = "vector.search.start"
	EventVectorSearchComplete = "vector.search.complete"

	// Request Events
	EventRequestReceived = "request.received"
	EventRequestComplete = "request.complete"

	// Agent runtime attributes
	AgentID                  = "agent.id"
	AgentName                = "agent.name"
	AgentModel               = "agent.model"
	AgentSessionID           = "agent.session.id"
	AgentTurnNumber          = "agent.turn.number"
	AgentIteration           = "agent.iteration"
	AgentToolsCount          = "agent.tools.count"
	AgentToolCallID          = "agent.tool_call.id"
	AgentToolCallName        = "agent.tool_call.name"
	AgentToolCallSuccess     = "agent.tool_call.success"
	AgentToolCallDurationMs  = "agent.tool_call.duration_ms"
	AgentFinishReason        = "agent.finish_reason"
	AgentTotalTurns          = "agent.total_turns"
	AgentTotalToolCalls      = "agent.total_tool_calls"
	AgentTotalTokens         = "agent.total_tokens"
	AgentExecutionMode       = "agent.execution_mode"
	AgentPersistenceMode     = "agent.persistence_mode"
	AgentSandboxEnabled      = "agent.sandbox_enabled"
	AgentGitRepoConfigured   = "agent.git_repo_configured"
	AgentTemplateConfigured  = "agent.template_configured"
	AgentTurnToolCalls       = "agent.turn.tool_calls"
	AgentTurnSandboxTools    = "agent.turn.sandbox_tool_calls"
	AgentTurnNonSandboxTools = "agent.turn.non_sandbox_tool_calls"
	AgentTurnToolErrors      = "agent.turn.tool_errors"

	// Per-turn outcome snapshot attributes — the inputs that produced the
	// turn's decision. Used by the verdict-rates breakdown queries (Phase 0c)
	// to slice outcomes by model + prompt template version + context size.
	AgentTurnPromptTemplateID    = "agent.turn.prompt_template_id"
	AgentTurnPromptVersion       = "agent.turn.prompt_version"
	AgentTurnReasoningTextHash   = "agent.turn.reasoning_text_hash"
	AgentTurnContextBytesAtStart = "agent.turn.context_bytes_at_decision"
	// AgentTurnToolResultSummary is a compact "ok=N err=M" string per tool
	// name, computed from the turn's emitted tool spans. Cardinality stays
	// bounded by the per-turn tool name set, not per-call.
	AgentTurnToolResultSummary = "agent.turn.tool_result_summary"

	AgentSessionReadyMs      = "agent.session_ready_ms"
	AgentMode                = "agent.mode"
	AgentTaskPermissionMode  = "agent.task_permission_mode"
	AgentMaxSteps            = "agent.max_steps"
	AgentWorkingDirectory    = "agent.working_directory"
	AgentDelegationDecision  = "agent.delegation.decision"

	// Sandbox git clone attributes
	SandboxGitCloneDurationMs = "sandbox.git.clone_duration_ms"
	SandboxGitCloneBytesTotal = "sandbox.git.clone_bytes_total"
	SandboxGitCloneStrategy   = "sandbox.git.clone_strategy"
	SandboxGitCloned          = "sandbox.git.cloned"

	// Agent span event names
	EventAgentSessionStart  = "agent.session.start"
	EventAgentSessionEnd    = "agent.session.end"
	EventAgentTurnStart     = "agent.turn.start"
	EventAgentTurnEnd       = "agent.turn.end"
	EventAgentToolCallStart = "agent.tool_call.start"
	EventAgentToolCallEnd   = "agent.tool_call.end"

	// Sandbox execution attributes
	SandboxID          = "sandbox.id"
	SandboxBackend     = "sandbox.backend"
	SandboxImage       = "sandbox.image"
	SandboxExitCode    = "sandbox.exit_code"
	SandboxDurationMs  = "sandbox.duration_ms"
	SandboxTimedOut    = "sandbox.timed_out"
	SandboxLanguage    = "sandbox.language"
	SandboxCommand     = "sandbox.command"      // Executed command (truncated)
	SandboxStdoutBytes = "sandbox.stdout_bytes" // Size of captured stdout
	SandboxStderrBytes = "sandbox.stderr_bytes" // Size of captured stderr
	SandboxFSPath      = "sandbox.fs.path"      // File path for an fs operation
	SandboxFSBytes     = "sandbox.fs.bytes"     // Bytes read / written
	SandboxFSEntries   = "sandbox.fs.entries"   // Directory entries listed / files deleted

	// Sandbox span event names
	EventSandboxCreate  = "sandbox.create"
	EventSandboxReady   = "sandbox.ready"
	EventSandboxExec    = "sandbox.exec"
	EventSandboxResult  = "sandbox.result"
	EventSandboxError   = "sandbox.error"
	EventSandboxDestroy = "sandbox.destroy"

	// MCP Gateway attributes
	McpServerID        = "mcp.server.id"
	McpServerName      = "mcp.server.name"
	McpServerTransport = "mcp.server.transport"
	McpToolName        = "mcp.tool.name"
	McpToolCallStatus  = "mcp.tool.call.status"
	McpToolCallLatency = "mcp.tool.call.latency_ms"
	McpToolCount       = "mcp.tool.count"
	McpHealthStatus    = "mcp.server.health_status"

	// MCP span event names
	EventMcpToolCall        = "mcp.tool.call"
	EventMcpServerDiscovery = "mcp.server.discovery"
	EventMcpHealthCheck     = "mcp.server.health_check"
)
