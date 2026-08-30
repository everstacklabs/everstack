-- Pre-aggregated hourly metrics
CREATE TABLE IF NOT EXISTS trace_metrics_hourly (
    tenant_id LowCardinality(String),
    period DateTime,
    model LowCardinality(String),
    provider LowCardinality(String),
    environment LowCardinality(String),
    status LowCardinality(String),
    request_count UInt64,
    error_count UInt64,
    total_input_tokens UInt64,
    total_output_tokens UInt64,
    total_cost Float64,
    sum_duration_ns UInt64,
    min_duration_ns UInt64,
    max_duration_ns UInt64
) ENGINE = SummingMergeTree()
ORDER BY (tenant_id, period, model, provider, environment, status)
TTL period + INTERVAL 90 DAY;

-- Session aggregation
CREATE TABLE IF NOT EXISTS trace_sessions (
    tenant_id LowCardinality(String),
    session_id String,
    user_id String DEFAULT '',
    first_trace_at DateTime64(3),
    last_trace_at DateTime64(3),
    trace_count UInt32,
    total_duration_ns UInt64,
    total_input_tokens UInt64,
    total_output_tokens UInt64,
    total_cost Float64,
    error_count UInt32,
    models Array(String),
    tags Array(String),
    environment LowCardinality(String) DEFAULT '',
    updated_at DateTime64(3)
) ENGINE = ReplacingMergeTree(updated_at)
ORDER BY (tenant_id, session_id);

-- User aggregation
CREATE TABLE IF NOT EXISTS trace_users (
    tenant_id LowCardinality(String),
    user_id String,
    first_seen DateTime64(3),
    last_seen DateTime64(3),
    session_count UInt32,
    trace_count UInt32,
    total_tokens UInt64,
    total_cost Float64,
    error_rate Float64,
    avg_latency_ns UInt64,
    updated_at DateTime64(3)
) ENGINE = ReplacingMergeTree(updated_at)
ORDER BY (tenant_id, user_id);
