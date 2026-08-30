-- Split request_count and sum_duration_ns aggregation by span kind.
--
-- Pre-fix: the MV emitted request_count=1 and sum_duration_ns=Duration for
-- BOTH `gateway.chat.completion`/`gateway.embeddings` AND every
-- `agent.turn.%` span. Agent turns can run multi-iteration tool loops for
-- minutes; rolling them into the same `avg_latency_ms = sum/count`
-- formula made the dashboard's latency number look like ~30s when the
-- chat-completion p50 was actually ~600ms. Same `total_requests` was
-- inflated for any tenant running agents.
--
-- Post-fix: request_count + sum_duration_ns count gateway spans only.
-- Agent turns get their own counters (agent_turn_count,
-- sum_agent_turn_duration_ns) so they remain visible without polluting
-- the user-facing latency. Tokens and cost still come from provider.%
-- spans only — that part was correct already.
--
-- Historical rows keep their old totals (we don't rewrite SummingMergeTree
-- parts) but new ingest is clean. Dashboards that read recent windows
-- recover within an hour; longer windows get a short transition period
-- where pre-cutover data still mixes the two kinds.

ALTER TABLE trace_metrics_hourly
    ADD COLUMN IF NOT EXISTS agent_turn_count UInt64 DEFAULT 0,
    ADD COLUMN IF NOT EXISTS sum_agent_turn_duration_ns UInt64 DEFAULT 0;

DROP VIEW IF EXISTS mv_trace_metrics;

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

    -- Gateway request count: only chat-completion + embeddings root spans.
    -- One row per user-facing API call.
    toUInt64(if(
        SpanName IN ('gateway.chat.completion', 'gateway.embeddings'),
        1, 0
    )) as request_count,

    -- Gateway error count: same set, gated on STATUS_CODE_ERROR.
    toUInt64(if(
        SpanName IN ('gateway.chat.completion', 'gateway.embeddings')
        AND StatusCode = 'STATUS_CODE_ERROR',
        1, 0
    )) as error_count,

    -- Tokens: provider spans only. Same as before.
    toUInt64(if(SpanName LIKE 'provider.%',
        toInt64OrZero(SpanAttributes['llm.tokens.input']), 0
    )) as total_input_tokens,

    toUInt64(if(SpanName LIKE 'provider.%',
        toInt64OrZero(SpanAttributes['llm.tokens.output']), 0
    )) as total_output_tokens,

    -- Cost: provider spans only. Same as before.
    if(SpanName LIKE 'provider.%',
        greatest(
            toFloat64OrZero(SpanAttributes['cost.estimated_usd']),
            toFloat64OrZero(SpanAttributes['llm.cost.total'])
        ), 0
    ) as total_cost,

    -- Gateway latency: sums durations of chat-completion + embeddings only.
    -- Combined with request_count above this gives correct user-facing
    -- avg_latency_ms.
    toUInt64(if(
        SpanName IN ('gateway.chat.completion', 'gateway.embeddings'),
        toInt64(Duration), 0
    )) as sum_duration_ns,

    -- Agent turn count: one row per agent loop turn.
    toUInt64(if(SpanName LIKE 'agent.turn.%', 1, 0)) as agent_turn_count,

    -- Agent turn latency: separate column so it doesn't pollute the
    -- gateway-latency formula. Includes the entire iterative tool loop.
    toUInt64(if(SpanName LIKE 'agent.turn.%', toInt64(Duration), 0)) as sum_agent_turn_duration_ns

FROM otel_traces
WHERE SpanName IN ('gateway.chat.completion', 'gateway.embeddings')
   OR SpanName LIKE 'agent.turn.%'
   OR SpanName LIKE 'provider.%';
