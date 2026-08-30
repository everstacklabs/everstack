-- Seed the durable public model-metrics fact table from retained raw telemetry.
--
-- The materialized view in model_metrics_hourly_20260728203000 only sees new
-- otel_traces inserts. Without a one-time backfill, a freshly deployed monthly
-- report starts empty even though ClickHouse still has recent production
-- traffic. Backfill only complete hours before the first hour already captured
-- by the MV, which avoids double counting the cutover hour. If the fact table is
-- still empty, use the start of the current hour as that boundary.
--
-- Raw trace retention is currently 30 days. Bounding the scan to the same
-- window keeps this migration predictable on established installations while
-- giving the public monthly view all recoverable history.
--
-- Older spans predate the explicit customer/internal marker and canonical
-- identity stamps. Treat an absent traffic marker as legacy customer traffic,
-- but only infer identity for direct providers. Legacy hosted/platform routes
-- are skipped unless they already carry a canonical stamp; guessing their
-- publisher from the serving provider would permanently misclassify them.

INSERT INTO model_metrics_hourly
WITH
    (
        SELECT if(
            count() = 0,
            toStartOfHour(now()),
            min(period)
        )
        FROM model_metrics_hourly
    ) AS backfill_before,
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
WHERE Timestamp >= now() - INTERVAL 30 DAY
  AND Timestamp < backfill_before
  AND SpanName LIKE 'provider.%'
  AND SpanAttributes['everstack.traffic.kind'] != 'internal'
  AND SpanAttributes['tenant.id'] != ''
  AND ResourceAttributes['tenant.type'] = 'cloud'
  AND ResourceAttributes['instance.owner'] = 'everstack'
  AND ResourceAttributes['deployment.environment'] = 'production'
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
  AND canonical_id != '';
