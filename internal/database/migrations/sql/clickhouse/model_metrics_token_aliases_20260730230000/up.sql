-- Preserve token volume across the OpenTelemetry semantic-convention aliases
-- seen in retained gateway spans. Native Everstack provider spans use
-- llm.tokens.*, while imported/older spans can use gen_ai.usage.*,
-- response.*, or the provider payload field names.

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
    toUInt64(StatusCode = 'STATUS_CODE_ERROR') AS error_count,
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

-- Insert token-only deltas for retained facts whose request counts were
-- already backfilled but whose token values lived under an older alias.
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
            ), 0) AS cache_write_count
        SELECT
            SpanAttributes['tenant.id'] AS tenant_id,
            toStartOfHour(Timestamp) AS period,
            serving_provider AS provider,
            model_publisher AS publisher,
            canonical_id AS canonical_model_id,
            served_model AS model,
            toUInt64(sum(input_count)) AS input_tokens,
            toUInt64(sum(output_count)) AS output_tokens,
            toUInt64(sum(reasoning_count)) AS reasoning_tokens,
            toUInt64(sum(cache_read_count)) AS cache_read_tokens,
            toUInt64(sum(cache_write_count)) AS cache_write_tokens
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
            sum(input_tokens) AS input_tokens,
            sum(output_tokens) AS output_tokens,
            sum(reasoning_tokens) AS reasoning_tokens,
            sum(cache_read_tokens) AS cache_read_tokens,
            sum(cache_write_tokens) AS cache_write_tokens
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
    toUInt64(0) AS error_count,
    toUInt64(if(
        raw.input_tokens > coalesce(existing.input_tokens, 0),
        raw.input_tokens - coalesce(existing.input_tokens, 0),
        0
    )) AS input_tokens,
    toUInt64(if(
        raw.output_tokens > coalesce(existing.output_tokens, 0),
        raw.output_tokens - coalesce(existing.output_tokens, 0),
        0
    )) AS output_tokens,
    toUInt64(if(
        raw.reasoning_tokens > coalesce(existing.reasoning_tokens, 0),
        raw.reasoning_tokens - coalesce(existing.reasoning_tokens, 0),
        0
    )) AS reasoning_tokens,
    toUInt64(if(
        raw.cache_read_tokens > coalesce(existing.cache_read_tokens, 0),
        raw.cache_read_tokens - coalesce(existing.cache_read_tokens, 0),
        0
    )) AS cache_read_tokens,
    toUInt64(if(
        raw.cache_write_tokens > coalesce(existing.cache_write_tokens, 0),
        raw.cache_write_tokens - coalesce(existing.cache_write_tokens, 0),
        0
    )) AS cache_write_tokens,
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
WHERE raw.input_tokens > coalesce(existing.input_tokens, 0)
   OR raw.output_tokens > coalesce(existing.output_tokens, 0)
   OR raw.reasoning_tokens > coalesce(existing.reasoning_tokens, 0)
   OR raw.cache_read_tokens > coalesce(existing.cache_read_tokens, 0)
   OR raw.cache_write_tokens > coalesce(existing.cache_write_tokens, 0);
