-- OTEL Metrics table (CUSTOMER DATABASE)
-- Companion to otel_logs / otel_traces (see otel_telemetry_init_20251016000000).
-- The original otel_telemetry_init migration created logs + traces but not
-- metrics, so the public OTLP /v1/metrics receiver had nowhere to write on
-- managed instances. This adds the flattened single-row-per-datapoint metrics
-- table (mirrors build/clickhouse-init.sql) used by the MetricsHandler.
--
-- Tenant attributes stored in ResourceAttributes['tenant.id'] (stamped by the
-- receiver from the authenticated API key; clients cannot impersonate).

CREATE TABLE IF NOT EXISTS otel_metrics (
    ResourceAttributes Map(LowCardinality(String), String) CODEC(ZSTD(1)),
    ResourceSchemaUrl String CODEC(ZSTD(1)),
    ScopeName String CODEC(ZSTD(1)),
    ScopeVersion String CODEC(ZSTD(1)),
    ScopeAttributes Map(LowCardinality(String), String) CODEC(ZSTD(1)),
    ScopeDroppedAttrCount UInt32,
    ScopeSchemaUrl String CODEC(ZSTD(1)),
    MetricName LowCardinality(String) CODEC(ZSTD(1)),
    MetricDescription String CODEC(ZSTD(1)),
    MetricUnit String CODEC(ZSTD(1)),
    Attributes Map(LowCardinality(String), String) CODEC(ZSTD(1)),
    StartTimeUnix DateTime64(9) CODEC(Delta, ZSTD(1)),
    TimeUnix DateTime64(9) CODEC(Delta, ZSTD(1)),
    Value Float64 CODEC(ZSTD(1)),
    Flags UInt32,
    Exemplars Nested (
        FilteredAttributes Map(LowCardinality(String), String),
        TimeUnix DateTime64(9),
        Value Float64,
        SpanId String,
        TraceId String
    ) CODEC(ZSTD(1)),
    AggTemp Int32,
    IsMonotonic Boolean,
    INDEX idx_res_attr_key mapKeys(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_res_attr_value mapValues(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_attr_key mapKeys(Attributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_attr_value mapValues(Attributes) TYPE bloom_filter(0.01) GRANULARITY 1
) ENGINE = MergeTree
PARTITION BY toDate(TimeUnix)
ORDER BY (MetricName, Attributes, toUnixTimestamp(TimeUnix))
TTL toDateTime(TimeUnix) + toIntervalDay(90)
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;
