-- Restore the prior MV which mixed gateway and agent.turn rows into
-- request_count and sum_duration_ns. Drop the agent-turn columns added
-- by up.sql.

DROP VIEW IF EXISTS mv_trace_metrics;

ALTER TABLE trace_metrics_hourly
    DROP COLUMN IF EXISTS agent_turn_count,
    DROP COLUMN IF EXISTS sum_agent_turn_duration_ns;

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

    toUInt64(if(
        SpanName IN ('gateway.chat.completion', 'gateway.embeddings')
        OR SpanName LIKE 'agent.turn.%',
        1, 0
    )) as request_count,

    toUInt64(if(
        (SpanName IN ('gateway.chat.completion', 'gateway.embeddings')
         OR SpanName LIKE 'agent.turn.%')
        AND StatusCode = 'STATUS_CODE_ERROR',
        1, 0
    )) as error_count,

    toUInt64(if(SpanName LIKE 'provider.%',
        toInt64OrZero(SpanAttributes['llm.tokens.input']), 0
    )) as total_input_tokens,

    toUInt64(if(SpanName LIKE 'provider.%',
        toInt64OrZero(SpanAttributes['llm.tokens.output']), 0
    )) as total_output_tokens,

    if(SpanName LIKE 'provider.%',
        greatest(
            toFloat64OrZero(SpanAttributes['cost.estimated_usd']),
            toFloat64OrZero(SpanAttributes['llm.cost.total'])
        ), 0
    ) as total_cost,

    toUInt64(if(
        SpanName IN ('gateway.chat.completion', 'gateway.embeddings')
        OR SpanName LIKE 'agent.turn.%',
        toInt64(Duration), 0
    )) as sum_duration_ns

FROM otel_traces
WHERE SpanName IN ('gateway.chat.completion', 'gateway.embeddings')
   OR SpanName LIKE 'agent.turn.%'
   OR SpanName LIKE 'provider.%';
