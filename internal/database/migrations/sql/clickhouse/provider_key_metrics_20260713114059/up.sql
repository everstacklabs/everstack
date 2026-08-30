-- Per-provider-API-key metrics rollup.
--
-- Deliberately SEPARATE from trace_metrics_hourly: that table merges root-span
-- metrics (request/error/latency) and provider-span metrics (tokens/cost) into
-- one row via a shared ORDER BY key. The provider API key attribute exists ONLY
-- on provider spans, so adding it to that table's key would split every merge
-- group and zero out per-key request/latency. This table is fed exclusively by
-- provider spans, so each row is self-consistent.
--
-- Semantics: request_count / error_count are UPSTREAM ATTEMPTS, not user-facing
-- requests. One provider span == one upstream call, so retries and fallback
-- attempts each count once. Internal calls (compaction summarizer, semantic-cache
-- embeddings) are excluded because they are not stamped with provider.api_key_id.
-- Cache hits produce no provider span and are likewise absent.
CREATE TABLE IF NOT EXISTS provider_key_metrics_hourly (
    tenant_id LowCardinality(String),
    period DateTime,
    provider LowCardinality(String),
    provider_api_key_id String,          -- uuid; plain String (LowCardinality degrades past ~10k distinct)
    model LowCardinality(String),
    environment LowCardinality(String),
    request_count UInt64,
    error_count UInt64,
    total_input_tokens UInt64,
    total_output_tokens UInt64,
    total_cost Float64,
    sum_duration_ns UInt64
) ENGINE = SummingMergeTree()
ORDER BY (tenant_id, period, provider, provider_api_key_id, model, environment)
TTL period + INTERVAL 90 DAY;

-- Materialized view: fires on every provider-span insert that carries a
-- provider API key id. Mirrors mv_trace_metrics' conditional-aggregation style
-- but scoped to provider spans and keyed additionally by provider_api_key_id.
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_provider_key_metrics
TO provider_key_metrics_hourly AS
SELECT
    SpanAttributes['tenant.id']           AS tenant_id,
    toStartOfHour(Timestamp)              AS period,
    SpanAttributes['provider']            AS provider,
    SpanAttributes['provider.api_key_id'] AS provider_api_key_id,
    coalesce(
        nullIf(SpanAttributes['model.served'], ''),
        nullIf(SpanAttributes['llm.response.model'], ''),
        nullIf(SpanAttributes['model.requested'], ''),
        nullIf(SpanAttributes['llm.request.model'], ''),
        ''
    )                                     AS model,
    ResourceAttributes['deployment.environment'] AS environment,

    toUInt64(1) AS request_count,
    toUInt64(if(StatusCode = 'STATUS_CODE_ERROR', 1, 0)) AS error_count,

    toUInt64(toInt64OrZero(SpanAttributes['llm.tokens.input']))  AS total_input_tokens,
    toUInt64(toInt64OrZero(SpanAttributes['llm.tokens.output'])) AS total_output_tokens,

    greatest(
        toFloat64OrZero(SpanAttributes['cost.estimated_usd']),
        toFloat64OrZero(SpanAttributes['llm.cost.total'])
    ) AS total_cost,

    toUInt64(toInt64(Duration)) AS sum_duration_ns

FROM otel_traces
WHERE SpanName LIKE 'provider.%'
  AND SpanAttributes['provider.api_key_id'] != '';
