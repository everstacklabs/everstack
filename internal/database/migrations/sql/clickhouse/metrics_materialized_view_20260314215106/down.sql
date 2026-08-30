DROP VIEW IF EXISTS mv_trace_metrics;
DROP TABLE IF EXISTS trace_metrics_hourly;

-- Restore original table
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
