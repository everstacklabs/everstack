-- Count errors under the OTel status spelling that otel_traces actually stores,
-- in the last two materialized views that still compare against the enum name.
--
-- Same defect as model_metrics_error_status_20260826075905, which fixed
-- mv_model_metrics_hourly and introduced internal/telemetry/otelstatus for the
-- Go readers. These two views were named in that commit as reported-not-fixed;
-- this is the fix.
--
--   mv_trace_metrics        (metrics_split_kinds_20260510010630)
--   mv_provider_key_metrics (provider_key_metrics_20260713114059)
--
-- Both derived error_count from
--
--     StatusCode = 'STATUS_CODE_ERROR'
--
-- but the OpenTelemetry collector exporter writes the SHORT spelling --
-- 'Ok' / 'Error' / 'Unset' -- and that is what every gateway-produced span
-- carries. The enum name is only ever written by the Everstack OTLP HTTP
-- handler (internal/api/http/otlp/handler.go), so BOTH forms are legitimately
-- reachable and a reader must accept either. Verified read-only against
-- production ClickHouse on 2026-09-01:
--
--   SELECT StatusCode, count() FROM otel_traces GROUP BY StatusCode
--       -> Unset 8647, Ok 660, Error 32          (no enum-name row at all)
--
--   provider.% spans carrying provider.api_key_id  -> 688
--     countIf(StatusCode = 'STATUS_CODE_ERROR')                    ->  0
--     countIf(lower(StatusCode) IN ('error','status_code_error'))  -> 30
--   SELECT sum(error_count) FROM provider_key_metrics_hourly       ->  0
--
--   gateway.chat.completion + gateway.embeddings spans retained    ->  2
--     both non-error under either spelling
--   SELECT sum(error_count) FROM trace_metrics_hourly              ->  0
--
-- So per-provider-key error_count has been structurally zero since that table
-- was created on 2026-07-13, and trace_metrics_hourly's gateway error_count
-- has been zero since 2026-03-14.
--
-- The comparison is now spelling-agnostic and case-insensitive (ClickHouse '='
-- and IN are both case-sensitive), matching internal/telemetry/otelstatus and
-- the predicate already used by mv_model_metrics_hourly and the issues store.
-- This is deliberately not a swap of one literal for another: a future change
-- of exporter cannot silently re-break it.

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

    -- Gateway error count: same set, gated on an error span status under
    -- either stored spelling.
    toUInt64(if(
        SpanName IN ('gateway.chat.completion', 'gateway.embeddings')
        AND lower(StatusCode) IN ('error', 'status_code_error'),
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

DROP VIEW IF EXISTS mv_provider_key_metrics;

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
    -- Upstream attempt errors, gated on an error span status under either
    -- stored spelling.
    toUInt64(if(lower(StatusCode) IN ('error', 'status_code_error'), 1, 0)) AS error_count,

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

-- Insert error-only deltas for rollup rows whose source spans are retained.
--
-- Why a delta rather than a rewrite: both rollups are SummingMergeTree tables
-- of additive facts, and a materialized view only ever sees new inserts. A
-- corrected view therefore cannot repair rows that were already summed with
-- error_count = 0. Each repair below recomputes the true per-hour error count
-- from the spans that are STILL in otel_traces, subtracts whatever the rollup
-- already holds for that exact key, and inserts only the positive remainder.
-- Every other measure is written as zero so the row contributes nothing but the
-- missing errors, and the `raw > existing` guard makes the migration
-- idempotent: a second run finds the deltas already applied and inserts
-- nothing.
--
-- SCOPE LIMIT, stated plainly: otel_traces has a 30-day TTL
-- (otel_retention_ttl_20260728191232), so only hours whose spans still exist
-- can be corrected. Rollup rows older than that are left exactly as they are
-- rather than guessed at, and they keep reading error_count = 0 forever.
-- Measured on production 2026-09-01:
--
--   * provider_key_metrics_hourly spans 2026-08-10..2026-08-30 and the oldest
--     retained span is 2026-08-10 22:59, so the whole table is inside the
--     window. The delta resolves to 30 errors across 13 rollup keys -- 27
--     openai + 3 anthropic -- and after it the rollup reconciles exactly with
--     the raw spans.
--
--   * trace_metrics_hourly spans 2026-06-04..2026-08-30 but only 2 gateway
--     root spans are still retained, neither of them an error, so this delta
--     resolves to ZERO rows on production today. It is kept because it is the
--     same repair, it is idempotent, and other environments (and any hour
--     between now and the deploy) may have retained gateway error spans. The
--     pre-retention gateway hours are NOT recoverable.

INSERT INTO trace_metrics_hourly (
    tenant_id,
    period,
    model,
    provider,
    environment,
    request_count,
    error_count,
    total_input_tokens,
    total_output_tokens,
    total_cost,
    sum_duration_ns,
    agent_turn_count,
    sum_agent_turn_duration_ns
)
WITH
    raw AS (
        SELECT
            SpanAttributes['tenant.id'] AS tenant_id,
            toStartOfHour(Timestamp) AS period,
            coalesce(
                nullIf(SpanAttributes['model.served'], ''),
                nullIf(SpanAttributes['llm.response.model'], ''),
                nullIf(SpanAttributes['model.requested'], ''),
                nullIf(SpanAttributes['llm.request.model'], ''),
                ''
            ) AS model,
            if(SpanAttributes['provider'] != '' AND SpanAttributes['provider'] != 'unknown',
               SpanAttributes['provider'], '') AS provider,
            ResourceAttributes['deployment.environment'] AS environment,
            toUInt64(countIf(lower(StatusCode) IN ('error', 'status_code_error'))) AS error_count
        FROM otel_traces
        WHERE Timestamp >= now() - INTERVAL 30 DAY
          AND SpanName IN ('gateway.chat.completion', 'gateway.embeddings')
        GROUP BY
            tenant_id,
            period,
            model,
            provider,
            environment
    ),
    existing AS (
        SELECT
            tenant_id,
            period,
            model,
            provider,
            environment,
            sum(error_count) AS error_count
        FROM trace_metrics_hourly
        WHERE period >= now() - INTERVAL 30 DAY
        GROUP BY
            tenant_id,
            period,
            model,
            provider,
            environment
    )
SELECT
    raw.tenant_id,
    raw.period,
    raw.model,
    raw.provider,
    raw.environment,
    toUInt64(0) AS request_count,
    toUInt64(raw.error_count - coalesce(existing.error_count, 0)) AS error_count,
    toUInt64(0) AS total_input_tokens,
    toUInt64(0) AS total_output_tokens,
    toFloat64(0) AS total_cost,
    toUInt64(0) AS sum_duration_ns,
    toUInt64(0) AS agent_turn_count,
    toUInt64(0) AS sum_agent_turn_duration_ns
FROM raw
LEFT JOIN existing USING (
    tenant_id,
    period,
    model,
    provider,
    environment
)
WHERE raw.error_count > coalesce(existing.error_count, 0);

INSERT INTO provider_key_metrics_hourly (
    tenant_id,
    period,
    provider,
    provider_api_key_id,
    model,
    environment,
    request_count,
    error_count,
    total_input_tokens,
    total_output_tokens,
    total_cost,
    sum_duration_ns
)
WITH
    raw AS (
        SELECT
            SpanAttributes['tenant.id'] AS tenant_id,
            toStartOfHour(Timestamp) AS period,
            SpanAttributes['provider'] AS provider,
            SpanAttributes['provider.api_key_id'] AS provider_api_key_id,
            coalesce(
                nullIf(SpanAttributes['model.served'], ''),
                nullIf(SpanAttributes['llm.response.model'], ''),
                nullIf(SpanAttributes['model.requested'], ''),
                nullIf(SpanAttributes['llm.request.model'], ''),
                ''
            ) AS model,
            ResourceAttributes['deployment.environment'] AS environment,
            toUInt64(countIf(lower(StatusCode) IN ('error', 'status_code_error'))) AS error_count
        FROM otel_traces
        WHERE Timestamp >= now() - INTERVAL 30 DAY
          AND SpanName LIKE 'provider.%'
          AND SpanAttributes['provider.api_key_id'] != ''
        GROUP BY
            tenant_id,
            period,
            provider,
            provider_api_key_id,
            model,
            environment
    ),
    existing AS (
        SELECT
            tenant_id,
            period,
            provider,
            provider_api_key_id,
            model,
            environment,
            sum(error_count) AS error_count
        FROM provider_key_metrics_hourly
        WHERE period >= now() - INTERVAL 30 DAY
        GROUP BY
            tenant_id,
            period,
            provider,
            provider_api_key_id,
            model,
            environment
    )
SELECT
    raw.tenant_id,
    raw.period,
    raw.provider,
    raw.provider_api_key_id,
    raw.model,
    raw.environment,
    toUInt64(0) AS request_count,
    toUInt64(raw.error_count - coalesce(existing.error_count, 0)) AS error_count,
    toUInt64(0) AS total_input_tokens,
    toUInt64(0) AS total_output_tokens,
    toFloat64(0) AS total_cost,
    toUInt64(0) AS sum_duration_ns
FROM raw
LEFT JOIN existing USING (
    tenant_id,
    period,
    provider,
    provider_api_key_id,
    model,
    environment
)
WHERE raw.error_count > coalesce(existing.error_count, 0);
