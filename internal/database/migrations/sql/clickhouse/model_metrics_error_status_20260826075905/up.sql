-- Record provider errors in the public model-metrics facts.
--
-- The materialized view has always derived error_count from
--   StatusCode = 'STATUS_CODE_ERROR'
-- but the span status stored in otel_traces is the SHORT OpenTelemetry
-- spelling: 'Ok' / 'Error' / 'Unset'. The enum-name spelling is what the
-- Everstack OTLP HTTP handler writes (internal/api/http/otlp/handler.go),
-- so BOTH forms are legitimately present depending on which ingest path a
-- span arrived through -- but no provider span has ever carried the enum
-- name. Verified against production:
--
--   SELECT DISTINCT StatusCode FROM otel_traces          -> Ok, Error, Unset
--   countIf(StatusCode = 'STATUS_CODE_ERROR')            -> 0
--   countIf(lower(StatusCode) IN ('error',
--                                 'status_code_error'))  -> 33   (provider.* spans)
--
-- So error_count has been structurally zero since the fact table was
-- created, and the public catalog has reported a success_rate of exactly
-- 1.0 for every model. The comparison is now spelling-agnostic and
-- case-insensitive (ClickHouse '=' is case-sensitive), matching the
-- predicate already used by the issues store, so a future change of
-- exporter cannot silently re-break it.

DROP VIEW IF EXISTS mv_model_metrics_hourly;

CREATE MATERIALIZED VIEW mv_model_metrics_hourly
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
    greatest(coalesce(
        nullIf(toInt64OrZero(SpanAttributes['llm.tokens.input']), 0),
        nullIf(toInt64OrZero(SpanAttributes['gen_ai.usage.input_tokens']), 0),
        nullIf(toInt64OrZero(SpanAttributes['gen_ai.usage.prompt_tokens']), 0),
        nullIf(toInt64OrZero(SpanAttributes['response.prompt_tokens']), 0),
        nullIf(toInt64OrZero(SpanAttributes['prompt_tokens']), 0),
        nullIf(toInt64OrZero(SpanAttributes['input_tokens']), 0),
        nullIf(toInt64OrZero(SpanAttributes['input_token_count']), 0),
        nullIf(toInt64OrZero(SpanAttributes['llm.token_count.prompt']), 0),
        nullIf(toInt64OrZero(SpanAttributes['llm.usage.prompt_tokens']), 0),
        0
    ), 0) AS input_count,
    greatest(coalesce(
        nullIf(toInt64OrZero(SpanAttributes['llm.tokens.output']), 0),
        nullIf(toInt64OrZero(SpanAttributes['gen_ai.usage.output_tokens']), 0),
        nullIf(toInt64OrZero(SpanAttributes['gen_ai.usage.completion_tokens']), 0),
        nullIf(toInt64OrZero(SpanAttributes['response.completion_tokens']), 0),
        nullIf(toInt64OrZero(SpanAttributes['completion_tokens']), 0),
        nullIf(toInt64OrZero(SpanAttributes['output_tokens']), 0),
        nullIf(toInt64OrZero(SpanAttributes['output_token_count']), 0),
        nullIf(toInt64OrZero(SpanAttributes['llm.token_count.completion']), 0),
        nullIf(toInt64OrZero(SpanAttributes['llm.usage.completion_tokens']), 0),
        0
    ), 0) AS output_count,
    greatest(coalesce(
        nullIf(toInt64OrZero(SpanAttributes['llm.tokens.reasoning']), 0),
        nullIf(toInt64OrZero(SpanAttributes['gen_ai.usage.reasoning_tokens']), 0),
        nullIf(toInt64OrZero(SpanAttributes['reasoning_tokens']), 0),
        0
    ), 0) AS reasoning_count,
    greatest(coalesce(
        nullIf(toInt64OrZero(SpanAttributes['llm.tokens.cache_read']), 0),
        nullIf(toInt64OrZero(SpanAttributes['llm.tokens.cached']), 0),
        nullIf(toInt64OrZero(SpanAttributes['gen_ai.usage.cache_read_input_tokens']), 0),
        nullIf(toInt64OrZero(SpanAttributes['cache_read_tokens']), 0),
        0
    ), 0) AS cache_read_count,
    greatest(coalesce(
        nullIf(toInt64OrZero(SpanAttributes['llm.tokens.cache_write']), 0),
        nullIf(toInt64OrZero(SpanAttributes['gen_ai.usage.cache_creation_input_tokens']), 0),
        nullIf(toInt64OrZero(SpanAttributes['cache_write_tokens']), 0),
        0
    ), 0) AS cache_write_count,
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
    toUInt64(lower(StatusCode) IN ('error', 'status_code_error')) AS error_count,
    toUInt64(input_count) AS input_tokens,
    toUInt64(output_count) AS output_tokens,
    toUInt64(reasoning_count) AS reasoning_tokens,
    toUInt64(cache_read_count) AS cache_read_tokens,
    toUInt64(cache_write_count) AS cache_write_tokens,
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

-- Insert error-only deltas for retained facts.
--
-- Why a delta rather than a rewrite: model_metrics_hourly is a
-- SummingMergeTree of additive facts and the view only ever sees new
-- inserts, so a corrected view cannot repair rows that were already
-- summed with error_count = 0. The repair therefore recomputes the true
-- per-hour error count from the spans that are STILL in otel_traces,
-- subtracts whatever the fact table already holds for that exact key, and
-- inserts only the positive remainder. Every other measure is written as
-- zero so this row contributes nothing but the missing errors, and the
-- `raw > existing` guard makes the migration idempotent: a second run
-- finds the deltas already applied and inserts nothing.
--
-- Scope limit, stated plainly: this can only correct hours whose spans
-- still exist. Reconciling the rollup against eligible spans hour by hour
-- shows 33 requests recorded by the two earlier model_metrics_backfill_*
-- migrations whose spans have since aged out of the 30-day raw retention
-- (six hours across 2026-07-26 and 2026-07-28). Their error status is
-- unrecoverable and is deliberately NOT guessed at here; every hour from
-- 2026-08-10 onward reconciles exactly.
INSERT INTO model_metrics_hourly
WITH
    raw AS (
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
            ) AS canonical_id
        SELECT
            SpanAttributes['tenant.id'] AS tenant_id,
            toStartOfHour(Timestamp) AS period,
            serving_provider AS provider,
            model_publisher AS publisher,
            canonical_id AS canonical_model_id,
            served_model AS model,
            toUInt64(countIf(lower(StatusCode) IN ('error', 'status_code_error'))) AS error_count
        FROM otel_traces
        WHERE Timestamp >= now() - INTERVAL 30 DAY
          AND SpanName LIKE 'provider.%'
          AND SpanAttributes['everstack.traffic.kind'] != 'internal'
          AND SpanAttributes['tenant.id'] != ''
          AND ResourceAttributes['tenant.type'] = 'cloud'
          AND ResourceAttributes['instance.owner'] = 'everstack'
          AND ResourceAttributes['deployment.environment'] IN ('production', '')
          AND (
              stamped_canonical != ''
              OR serving_provider NOT IN (
                  'azure-openai',
                  'aws-bedrock',
                  'vertex-ai',
                  'fireworks',
                  'openrouter',
                  'huggingface',
                  'together',
                  'nvidia-nim'
              )
          )
          AND serving_provider != ''
          AND served_model != ''
          AND canonical_id != ''
        GROUP BY
            tenant_id,
            period,
            provider,
            publisher,
            canonical_model_id,
            model
    ),
    existing AS (
        SELECT
            tenant_id,
            period,
            provider,
            publisher,
            canonical_model_id,
            model,
            sum(error_count) AS error_count
        FROM model_metrics_hourly
        WHERE period >= now() - INTERVAL 30 DAY
        GROUP BY
            tenant_id,
            period,
            provider,
            publisher,
            canonical_model_id,
            model
    )
SELECT
    raw.tenant_id,
    raw.period,
    raw.provider,
    raw.publisher,
    raw.canonical_model_id,
    raw.model,
    toUInt64(0) AS request_count,
    toUInt64(if(
        raw.error_count > coalesce(existing.error_count, 0),
        raw.error_count - coalesce(existing.error_count, 0),
        0
    )) AS error_count,
    toUInt64(0) AS input_tokens,
    toUInt64(0) AS output_tokens,
    toUInt64(0) AS reasoning_tokens,
    toUInt64(0) AS cache_read_tokens,
    toUInt64(0) AS cache_write_tokens,
    toFloat64(0) AS total_cost_usd,
    toFloat64(0) AS latency_total_ms,
    toUInt64(0) AS latency_samples,
    toFloat64(0) AS ttft_total_ms,
    toUInt64(0) AS ttft_samples,
    toUInt64(0) AS stream_output_tokens,
    toFloat64(0) AS generation_duration_ms
FROM raw
LEFT JOIN existing USING (
    tenant_id,
    period,
    provider,
    publisher,
    canonical_model_id,
    model
)
WHERE raw.error_count > coalesce(existing.error_count, 0);
