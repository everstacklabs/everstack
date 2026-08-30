-- Reverse tenant_id addition: recreate MVs without tenant_id, then drop columns.

DROP VIEW IF EXISTS mv_model_usage_hourly_mv;

CREATE MATERIALIZED VIEW IF NOT EXISTS mv_model_usage_hourly_mv TO mv_model_usage_hourly AS
SELECT
  JSONExtractString(payload, 'provider') AS provider,
  JSONExtractString(payload, 'model') AS model,
  toStartOfHour(toDateTime(created_at)) AS period,
  toUInt64(1) AS request_count,
  toUInt64(JSONExtractBool(payload, 'success')) AS success_count,
  toUInt64(NOT JSONExtractBool(payload, 'success')) AS error_count,
  toFloat64(JSONExtractFloat(payload, 'latency_ms')) AS sum_latency_ms,
  toFloat64(JSONExtractFloat(payload, 'latency_ms')) AS min_latency_ms,
  toFloat64(JSONExtractFloat(payload, 'latency_ms')) AS max_latency_ms,
  toUInt64(JSONExtractInt(payload, 'tokens_used')) AS total_tokens_used,
  toFloat64(JSONExtractFloat(payload, 'cost')) AS total_cost,
  toDateTime(created_at) AS updated_at
FROM events
WHERE type IN ('chat.session.started','embedding.request.completed');

DROP VIEW IF EXISTS mv_lb_hourly_mv;

CREATE MATERIALIZED VIEW IF NOT EXISTS mv_lb_hourly_mv TO mv_lb_hourly AS
SELECT
  toStartOfHour(toDateTime(created_at)) AS period,
  JSONExtractString(payload, 'strategy') AS strategy,
  JSONExtractString(payload, 'key_source') AS key_source,
  toUInt64(1) AS request_count,
  toUInt64(JSONExtractBool(payload, 'fallback_used')) AS fallback_count,
  toFloat64(JSONExtractFloat(payload, 'latency_ms')) AS sum_latency_ms,
  toUInt64(JSONExtractBool(payload, 'primary_success')) AS primary_success,
  toUInt64(JSONExtractBool(payload, 'fallback_success')) AS fallback_success,
  toUInt64(NOT JSONExtractBool(payload, 'success')) AS total_failures,
  toDateTime(created_at) AS updated_at
FROM events
WHERE type = 'load_balancer.request.completed';

DROP VIEW IF EXISTS mv_api_key_usage_hourly_mv;

CREATE MATERIALIZED VIEW IF NOT EXISTS mv_api_key_usage_hourly_mv TO mv_api_key_usage_hourly AS
SELECT
  JSONExtractString(payload, 'api_key_hash') AS api_key_hash,
  JSONExtractString(payload, 'user_id') AS user_id,
  toStartOfHour(toDateTime(created_at)) AS period,
  sum(toUInt64(1)) AS request_count,
  sum(toUInt64(JSONExtractInt(payload, 'tokens_used'))) AS tokens_used,
  sum(toFloat64(JSONExtractFloat(payload, 'latency_ms'))) AS sum_latency_ms,
  sum(toUInt64(NOT JSONExtractBool(payload, 'success'))) AS error_count,
  max(toDateTime(created_at)) AS last_activity_at,
  max(toDateTime(created_at)) AS updated_at,
  uniqCombined64State(JSONExtractString(payload, 'session_id')) AS unique_sessions_state
FROM events
WHERE type IN ('chat.session.started','embedding.request.started')
  AND JSONExtractString(payload, 'api_key_hash') IS NOT NULL
GROUP BY api_key_hash, user_id, period;

DROP VIEW IF EXISTS mv_chat_sessions;

CREATE MATERIALIZED VIEW IF NOT EXISTS mv_chat_sessions TO chat_sessions AS
SELECT
  JSONExtractString(payload, 'session_id') AS id,
  JSONExtractString(payload, 'user_id') AS user_id,
  JSONExtractString(payload, 'api_key') AS api_key,
  JSONExtractString(payload, 'model') AS model,
  JSONExtractString(payload, 'provider') AS provider,
  JSONExtractInt(payload, 'message_count') AS message_count,
  JSONExtractInt(payload, 'tokens_used') AS tokens_used,
  parseDateTimeBestEffortOrNull(JSONExtractString(payload, 'started_at')) AS started_at,
  parseDateTimeBestEffortOrNull(JSONExtractString(payload, 'completed_at')) AS completed_at,
  toUInt64(JSONExtractInt(payload, 'duration_ms')) AS duration,
  toUInt8(JSONExtractBool(payload, 'success')) AS success,
  JSONExtractString(payload, 'error_code') AS error_code,
  JSONExtractString(payload, 'error_message') AS error_message,
  JSONExtractRaw(payload, 'metadata') AS metadata,
  JSONExtractString(payload, 'correlation_id') AS correlation_id,
  toDateTime(created_at) AS created_at
FROM events
WHERE type = 'chat.session.started';

ALTER TABLE events DROP COLUMN IF EXISTS tenant_id;

ALTER TABLE event_blobs DROP COLUMN IF EXISTS tenant_id;

ALTER TABLE chat_sessions DROP COLUMN IF EXISTS tenant_id;

ALTER TABLE mv_model_usage_hourly DROP COLUMN IF EXISTS tenant_id;

ALTER TABLE mv_lb_hourly DROP COLUMN IF EXISTS tenant_id;

ALTER TABLE mv_api_key_usage_hourly DROP COLUMN IF EXISTS tenant_id;
