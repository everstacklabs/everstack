-- Rich Tracing Extensions for Langfuse Compatibility
-- Adds scores table and enriched trace views

-- OTEL Trace Scores table (for evaluation and quality metrics)
-- Supports Langfuse score types: Numeric, Categorical, Boolean
CREATE TABLE IF NOT EXISTS otel_trace_scores (
    ScoreId String CODEC(ZSTD(1)),
    TraceId String CODEC(ZSTD(1)),
    ObservationId String CODEC(ZSTD(1)), -- Optional: can link to specific span
    Timestamp DateTime64(9) CODEC(Delta(8), ZSTD(1)),
    CreatedAt DateTime64(9) CODEC(Delta(8), ZSTD(1)),
    UpdatedAt DateTime64(9) CODEC(Delta(8), ZSTD(1)),
    
    -- Score metadata
    Name LowCardinality(String) CODEC(ZSTD(1)),
    Source LowCardinality(String) CODEC(ZSTD(1)), -- ANNOTATION, API, EVAL
    DataType LowCardinality(String) CODEC(ZSTD(1)), -- NUMERIC, CATEGORICAL, BOOLEAN
    
    -- Score values (only one will be populated based on DataType)
    NumericValue Nullable(Float64) CODEC(ZSTD(1)),
    StringValue Nullable(String) CODEC(ZSTD(1)),
    BooleanValue Nullable(UInt8) CODEC(ZSTD(1)),
    
    -- Additional metadata
    Comment String CODEC(ZSTD(1)),
    AuthorUserId String CODEC(ZSTD(1)),
    ConfigId String CODEC(ZSTD(1)), -- Reference to score config
    QueueId String CODEC(ZSTD(1)), -- Reference to annotation queue
    Metadata String CODEC(ZSTD(1)), -- JSON metadata
    Environment LowCardinality(String) CODEC(ZSTD(1)),
    
    INDEX idx_trace_id TraceId TYPE bloom_filter(0.001) GRANULARITY 1,
    INDEX idx_observation_id ObservationId TYPE bloom_filter(0.001) GRANULARITY 1,
    INDEX idx_score_name Name TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_source Source TYPE bloom_filter(0.01) GRANULARITY 1
) ENGINE = MergeTree()
PARTITION BY toDate(Timestamp)
ORDER BY (TraceId, Name, toUnixTimestamp(Timestamp), ScoreId)
TTL toDateTime(Timestamp) + INTERVAL 90 DAY
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

-- Enriched Trace View (materializes commonly queried trace attributes)
-- Extracts session_id, user_id, tags, model info from attributes
CREATE MATERIALIZED VIEW IF NOT EXISTS trace_details_view
ENGINE = MergeTree()
PARTITION BY toDate(Timestamp)
ORDER BY (TraceId, Timestamp)
TTL toDateTime(Timestamp) + INTERVAL 7 DAY
SETTINGS index_granularity = 8192
AS SELECT
    Timestamp,
    TraceId,
    SpanId,
    ParentSpanId,
    SpanName,
    SpanKind,
    ServiceName,
    Duration,
    StatusCode,
    StatusMessage,
    
    -- Extract common attributes from maps
    SpanAttributes['trace.name'] as TraceName,
    SpanAttributes['trace.user_id'] as UserId,
    SpanAttributes['trace.session_id'] as SessionId,
    SpanAttributes['trace.tags'] as Tags,
    SpanAttributes['trace.input'] as Input,
    SpanAttributes['trace.output'] as Output,
    SpanAttributes['trace.metadata'] as Metadata,
    
    -- Resource attributes
    ResourceAttributes['service.version'] as ServiceVersion,
    ResourceAttributes['deployment.environment'] as Environment,
    ResourceAttributes['release'] as Release,
    
    -- LLM-specific attributes
    SpanAttributes['llm.model'] as Model,
    SpanAttributes['llm.provider'] as Provider,
    SpanAttributes['model.requested'] as RequestedModel,
    SpanAttributes['model.served'] as ServedModel,
    
    -- Token usage
    toInt64OrNull(SpanAttributes['llm.tokens.input']) as InputTokens,
    toInt64OrNull(SpanAttributes['llm.tokens.output']) as OutputTokens,
    toInt64OrNull(SpanAttributes['llm.tokens.total']) as TotalTokens,
    
    -- Cost information
    toFloat64OrNull(SpanAttributes['llm.cost.input']) as InputCost,
    toFloat64OrNull(SpanAttributes['llm.cost.output']) as OutputCost,
    toFloat64OrNull(SpanAttributes['llm.cost.total']) as TotalCost,
    
    -- Observation type (Langfuse compatibility)
    SpanAttributes['observation.type'] as ObservationType,
    SpanAttributes['observation.level'] as ObservationLevel,
    
    -- Request/Response details (JSON serialized)
    SpanAttributes['llm.request.messages'] as RequestMessages,
    SpanAttributes['llm.request.model_parameters'] as ModelParameters,
    SpanAttributes['llm.response.choices'] as ResponseChoices,
    SpanAttributes['llm.response.finish_reason'] as FinishReason,
    
    -- All attributes for fallback
    SpanAttributes,
    ResourceAttributes
FROM otel_traces;

-- Add indexes to base traces table for common query patterns
-- Note: These are added as ALTER statements for safety with existing data
ALTER TABLE otel_traces ADD INDEX IF NOT EXISTS idx_user_id (SpanAttributes['trace.user_id']) TYPE bloom_filter(0.01) GRANULARITY 1;
ALTER TABLE otel_traces ADD INDEX IF NOT EXISTS idx_session_id (SpanAttributes['trace.session_id']) TYPE bloom_filter(0.01) GRANULARITY 1;
ALTER TABLE otel_traces ADD INDEX IF NOT EXISTS idx_model (SpanAttributes['llm.model']) TYPE bloom_filter(0.01) GRANULARITY 1;
ALTER TABLE otel_traces ADD INDEX IF NOT EXISTS idx_observation_type (SpanAttributes['observation.type']) TYPE bloom_filter(0.01) GRANULARITY 1;


