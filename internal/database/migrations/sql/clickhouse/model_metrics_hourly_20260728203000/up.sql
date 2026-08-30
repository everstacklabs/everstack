-- Durable, tenant-keyed model activity facts for the public catalog.
--
-- This is intentionally NOT the public response table. Tenant identity remains
-- in ClickHouse so the read-side privacy boundary can require a minimum number
-- of distinct tenants per returned bucket. The public API never returns a
-- tenant identifier and never queries raw otel_traces.
--
-- Semantics:
--   * one provider span = one upstream attempt (retries/fallbacks count)
--   * only managed Everstack production traffic is included
--   * internal calls (cache embeddings, eval/scorer traffic, etc.) are excluded
--   * cumulative values are derived at read time from these additive facts
--   * no TTL: "all time" and monthly usage must survive raw-telemetry expiry

CREATE TABLE IF NOT EXISTS model_metrics_hourly (
    tenant_id String CODEC(ZSTD(1)),
    period DateTime CODEC(Delta(4), ZSTD(1)),
    provider LowCardinality(String) CODEC(ZSTD(1)),
    publisher LowCardinality(String) CODEC(ZSTD(1)),
    canonical_model_id String CODEC(ZSTD(1)),
    model String CODEC(ZSTD(1)),

    request_count UInt64,
    error_count UInt64,
    input_tokens UInt64,
    output_tokens UInt64,
    reasoning_tokens UInt64,
    cache_read_tokens UInt64,
    cache_write_tokens UInt64,
    total_cost_usd Float64,

    latency_total_ms Float64,
    latency_samples UInt64,
    ttft_total_ms Float64,
    ttft_samples UInt64,
    stream_output_tokens UInt64,
    generation_duration_ms Float64,

    -- Keep the migration compatible with the ClickHouse versions already used
    -- by self-hosted installations. These indexes accelerate the three public
    -- dimensions without requiring projection deduplication support.
    INDEX idx_provider provider TYPE set(0) GRANULARITY 4,
    INDEX idx_publisher publisher TYPE set(0) GRANULARITY 4,
    INDEX idx_canonical_model canonical_model_id TYPE bloom_filter(0.01) GRANULARITY 4
) ENGINE = SummingMergeTree()
PARTITION BY toYYYYMM(period)
ORDER BY (period, provider, publisher, canonical_model_id, tenant_id, model)
SETTINGS index_granularity = 8192;

CREATE MATERIALIZED VIEW IF NOT EXISTS mv_model_metrics_hourly
TO model_metrics_hourly AS
WITH
    lowerUTF8(SpanAttributes['provider']) AS serving_provider,
    coalesce(
        nullIf(SpanAttributes['model.served'], ''),
        nullIf(SpanAttributes['llm.response.model'], ''),
        nullIf(SpanAttributes['model.requested'], ''),
        nullIf(SpanAttributes['llm.request.model'], ''),
        ''
    ) AS served_model,
    lowerUTF8(SpanAttributes['model.publisher']) AS stamped_publisher,
    if(stamped_publisher != '', stamped_publisher, serving_provider) AS model_publisher,
    lowerUTF8(SpanAttributes['model.canonical_id']) AS stamped_canonical,
    if(
        stamped_canonical != '',
        stamped_canonical,
        concat(model_publisher, '/', lowerUTF8(served_model))
    ) AS canonical_id,
    greatest(toInt64OrZero(SpanAttributes['llm.tokens.input']), 0) AS input_count,
    greatest(toInt64OrZero(SpanAttributes['llm.tokens.output']), 0) AS output_count,
    greatest(toFloat64OrZero(SpanAttributes['llm.stream.time_to_first_token_ms']), 0) AS ttft_ms,
    greatest(toFloat64OrZero(SpanAttributes['llm.stream.total_latency_ms']), 0) AS stream_latency_ms
SELECT
    SpanAttributes['tenant.id'] AS tenant_id,
    toStartOfHour(Timestamp) AS period,
    serving_provider AS provider,
    model_publisher AS publisher,
    canonical_id AS canonical_model_id,
    served_model AS model,

    toUInt64(1) AS request_count,
    toUInt64(StatusCode = 'STATUS_CODE_ERROR') AS error_count,
    toUInt64(input_count) AS input_tokens,
    toUInt64(output_count) AS output_tokens,
    toUInt64(greatest(toInt64OrZero(SpanAttributes['llm.tokens.reasoning']), 0)) AS reasoning_tokens,
    toUInt64(greatest(
        toInt64OrZero(SpanAttributes['llm.tokens.cache_read']),
        toInt64OrZero(SpanAttributes['llm.tokens.cached']),
        0
    )) AS cache_read_tokens,
    toUInt64(greatest(toInt64OrZero(SpanAttributes['llm.tokens.cache_write']), 0)) AS cache_write_tokens,
    greatest(
        toFloat64OrZero(SpanAttributes['cost.estimated_usd']),
        toFloat64OrZero(SpanAttributes['llm.cost.total']),
        0
    ) AS total_cost_usd,

    greatest(toFloat64(Duration) / 1e6, 0) AS latency_total_ms,
    toUInt64(1) AS latency_samples,
    if(SpanAttributes['llm.stream.time_to_first_token_ms'] != '', ttft_ms, 0) AS ttft_total_ms,
    toUInt64(SpanAttributes['llm.stream.time_to_first_token_ms'] != '') AS ttft_samples,
    toUInt64(if(SpanAttributes['llm.stream.total_latency_ms'] != '', output_count, 0)) AS stream_output_tokens,
    if(
        SpanAttributes['llm.stream.total_latency_ms'] != '',
        greatest(stream_latency_ms - ttft_ms, 0),
        0
    ) AS generation_duration_ms
FROM otel_traces
WHERE SpanName LIKE 'provider.%'
  AND SpanAttributes['everstack.traffic.kind'] = 'customer'
  AND SpanAttributes['tenant.id'] != ''
  AND ResourceAttributes['tenant.type'] = 'cloud'
  AND ResourceAttributes['instance.owner'] = 'everstack'
  AND ResourceAttributes['deployment.environment'] = 'production'
  AND serving_provider != ''
  AND served_model != ''
  AND canonical_id != '';
