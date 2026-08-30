CREATE TABLE IF NOT EXISTS otel_trace_overlays (
    TenantId String CODEC(ZSTD(1)),
    TraceId String CODEC(ZSTD(1)),
    UpdatedAt DateTime64(9) CODEC(Delta(8), ZSTD(1)),
    AuthorUserId String CODEC(ZSTD(1)),
    DisplayName Nullable(String) CODEC(ZSTD(1)),
    InputOverride Nullable(String) CODEC(ZSTD(1)),
    OutputOverride Nullable(String) CODEC(ZSTD(1)),
    Metadata String CODEC(ZSTD(1)),
    Tags Array(String) CODEC(ZSTD(1)),
    HiddenSpanIds Array(String) CODEC(ZSTD(1)),
    INDEX idx_trace_overlay_trace_id TraceId TYPE bloom_filter(0.001) GRANULARITY 1,
    INDEX idx_trace_overlay_tenant TenantId TYPE bloom_filter(0.01) GRANULARITY 1
) ENGINE = MergeTree()
PARTITION BY toDate(UpdatedAt)
ORDER BY (TenantId, TraceId, toUnixTimestamp(UpdatedAt))
TTL toDateTime(UpdatedAt) + INTERVAL 90 DAY
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

CREATE TABLE IF NOT EXISTS otel_custom_observations (
    TenantId String CODEC(ZSTD(1)),
    ObservationId String CODEC(ZSTD(1)),
    TraceId String CODEC(ZSTD(1)),
    ParentObservationId String CODEC(ZSTD(1)),
    Name String CODEC(ZSTD(1)),
    Type LowCardinality(String) CODEC(ZSTD(1)),
    Source LowCardinality(String) CODEC(ZSTD(1)),
    StartTime DateTime64(9) CODEC(Delta(8), ZSTD(1)),
    EndTime Nullable(DateTime64(9)) CODEC(Delta(8), ZSTD(1)),
    Duration Int64 CODEC(ZSTD(1)),
    Level LowCardinality(String) CODEC(ZSTD(1)),
    StatusMessage String CODEC(ZSTD(1)),
    Model String CODEC(ZSTD(1)),
    InputData String CODEC(ZSTD(1)),
    OutputData String CODEC(ZSTD(1)),
    InputMimeType String CODEC(ZSTD(1)),
    OutputMimeType String CODEC(ZSTD(1)),
    InputTokens Nullable(Int64) CODEC(ZSTD(1)),
    OutputTokens Nullable(Int64) CODEC(ZSTD(1)),
    TotalTokens Nullable(Int64) CODEC(ZSTD(1)),
    InputCost Nullable(Float64) CODEC(ZSTD(1)),
    OutputCost Nullable(Float64) CODEC(ZSTD(1)),
    TotalCost Nullable(Float64) CODEC(ZSTD(1)),
    Metadata String CODEC(ZSTD(1)),
    Tags Array(String) CODEC(ZSTD(1)),
    AuthorUserId String CODEC(ZSTD(1)),
    CreatedAt DateTime64(9) CODEC(Delta(8), ZSTD(1)),
    UpdatedAt DateTime64(9) CODEC(Delta(8), ZSTD(1)),
    INDEX idx_custom_obs_trace_id TraceId TYPE bloom_filter(0.001) GRANULARITY 1,
    INDEX idx_custom_obs_tenant TenantId TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_custom_obs_type Type TYPE bloom_filter(0.01) GRANULARITY 1
) ENGINE = MergeTree()
PARTITION BY toDate(CreatedAt)
ORDER BY (TenantId, TraceId, StartTime, ObservationId)
TTL toDateTime(CreatedAt) + INTERVAL 90 DAY
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

CREATE TABLE IF NOT EXISTS otel_trace_annotations (
    TenantId String CODEC(ZSTD(1)),
    AnnotationId String CODEC(ZSTD(1)),
    TraceId String CODEC(ZSTD(1)),
    ObservationId String CODEC(ZSTD(1)),
    Body String CODEC(ZSTD(1)),
    Metadata String CODEC(ZSTD(1)),
    AuthorUserId String CODEC(ZSTD(1)),
    CreatedAt DateTime64(9) CODEC(Delta(8), ZSTD(1)),
    INDEX idx_trace_annotation_trace_id TraceId TYPE bloom_filter(0.001) GRANULARITY 1,
    INDEX idx_trace_annotation_observation_id ObservationId TYPE bloom_filter(0.001) GRANULARITY 1,
    INDEX idx_trace_annotation_tenant TenantId TYPE bloom_filter(0.01) GRANULARITY 1
) ENGINE = MergeTree()
PARTITION BY toDate(CreatedAt)
ORDER BY (TenantId, TraceId, CreatedAt, AnnotationId)
TTL toDateTime(CreatedAt) + INTERVAL 90 DAY
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;
