-- Drop the old empty table (no data, no MV was populating it)
DROP TABLE IF EXISTS trace_metrics_hourly;

-- Recreated with cleaner schema: removed min/max duration (SummingMergeTree sums them incorrectly),
-- removed status from ORDER BY (error_count is sufficient).
CREATE TABLE IF NOT EXISTS trace_metrics_hourly (
    tenant_id LowCardinality(String),
    period DateTime,
    model LowCardinality(String),
    provider LowCardinality(String),
    environment LowCardinality(String),
    request_count UInt64,
    error_count UInt64,
    total_input_tokens UInt64,
    total_output_tokens UInt64,
    total_cost Float64,
    sum_duration_ns UInt64
) ENGINE = SummingMergeTree()
ORDER BY (tenant_id, period, model, provider, environment)
TTL period + INTERVAL 90 DAY;

-- Materialized view: fires on every span insert, uses conditional aggregation
-- to extract the right metrics from the right span types:
--   - Gateway root spans + agent turn spans -> request_count, error_count, latency
--   - Provider spans -> tokens, cost
-- Both insert into the same SummingMergeTree; keys that match get merged automatically.
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_trace_metrics TO trace_metrics_hourly AS
SELECT
    SpanAttributes['tenant.id'] as tenant_id,
    toStartOfHour(Timestamp) as period,
    coalesce(
        nullIf(SpanAttributes['model.served'], ''),
        nullIf(SpanAttributes['llm.response.model'], ''),
        nullIf(SpanAttributes['model.requested'], ''),
        nullIf(SpanAttributes['llm.request.model'], ''),
        ''
    ) as model,
    if(SpanAttributes['provider'] != '' AND SpanAttributes['provider'] != 'unknown',
       SpanAttributes['provider'], '') as provider,
    ResourceAttributes['deployment.environment'] as environment,

    -- Request count: only from root spans (one per user-facing request)
    toUInt64(if(
        SpanName IN ('gateway.chat.completion', 'gateway.embeddings')
        OR SpanName LIKE 'agent.turn.%',
        1, 0
    )) as request_count,

    -- Error count: only from root spans
    toUInt64(if(
        (SpanName IN ('gateway.chat.completion', 'gateway.embeddings')
         OR SpanName LIKE 'agent.turn.%')
        AND StatusCode = 'STATUS_CODE_ERROR',
        1, 0
    )) as error_count,

    -- Tokens: only from provider spans (canonical source, avoids double-counting with root spans)
    toUInt64(if(SpanName LIKE 'provider.%',
        toInt64OrZero(SpanAttributes['llm.tokens.input']), 0
    )) as total_input_tokens,

    toUInt64(if(SpanName LIKE 'provider.%',
        toInt64OrZero(SpanAttributes['llm.tokens.output']), 0
    )) as total_output_tokens,

    -- Cost: only from provider spans
    if(SpanName LIKE 'provider.%',
        greatest(
            toFloat64OrZero(SpanAttributes['cost.estimated_usd']),
            toFloat64OrZero(SpanAttributes['llm.cost.total'])
        ), 0
    ) as total_cost,

    -- Latency: only from root spans (end-to-end latency)
    toUInt64(if(
        SpanName IN ('gateway.chat.completion', 'gateway.embeddings')
        OR SpanName LIKE 'agent.turn.%',
        toInt64(Duration), 0
    )) as sum_duration_ns

FROM otel_traces
WHERE SpanName IN ('gateway.chat.completion', 'gateway.embeddings')
   OR SpanName LIKE 'agent.turn.%'
   OR SpanName LIKE 'provider.%';
