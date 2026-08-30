-- Add tenant_id column to core event tables for shared-database multi-tenancy.
-- Self-hosted instances will have tenant_id = '' (the DEFAULT), which is backward compatible.

ALTER TABLE events ADD COLUMN IF NOT EXISTS tenant_id String DEFAULT '' AFTER stream;

ALTER TABLE event_blobs ADD COLUMN IF NOT EXISTS tenant_id String DEFAULT '' AFTER blob_id;

ALTER TABLE chat_sessions ADD COLUMN IF NOT EXISTS tenant_id String DEFAULT '' AFTER id;

-- Materialized view target tables: add tenant_id as first column so it
-- participates in the ORDER BY key for efficient filtering.
ALTER TABLE mv_model_usage_hourly ADD COLUMN IF NOT EXISTS tenant_id String DEFAULT '' FIRST;

ALTER TABLE mv_lb_hourly ADD COLUMN IF NOT EXISTS tenant_id String DEFAULT '' FIRST;

ALTER TABLE mv_api_key_usage_hourly ADD COLUMN IF NOT EXISTS tenant_id String DEFAULT '' FIRST;

-- Recreate materialized views to propagate tenant_id from events into the
-- target tables. ClickHouse requires DROP + CREATE (no ALTER on MVs).

DROP VIEW IF EXISTS mv_model_usage_hourly_mv;

CREATE MATERIALIZED VIEW IF NOT EXISTS mv_model_usage_hourly_mv TO mv_model_usage_hourly AS
SELECT
  tenant_id,
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
  tenant_id,
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
  tenant_id,
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
GROUP BY tenant_id, api_key_hash, user_id, period;

DROP VIEW IF EXISTS mv_chat_sessions;

CREATE MATERIALIZED VIEW IF NOT EXISTS mv_chat_sessions TO chat_sessions AS
SELECT
  tenant_id,
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

-- Recreate read views to include tenant_id for query filtering.

DROP VIEW IF EXISTS model_usage_stats_view;

CREATE VIEW IF NOT EXISTS model_usage_stats_view AS
SELECT
  tenant_id,
  provider,
  model,
  period,
  request_count,
  success_count,
  error_count,
  if(request_count = 0, 0.0, sum_latency_ms / toFloat64(request_count)) AS avg_latency_ms,
  min_latency_ms,
  max_latency_ms,
  total_tokens_used,
  total_cost,
  updated_at
FROM mv_model_usage_hourly FINAL;

DROP VIEW IF EXISTS load_balancer_stats_view;

CREATE VIEW IF NOT EXISTS load_balancer_stats_view AS
SELECT
  tenant_id,
  period,
  strategy,
  key_source,
  request_count,
  fallback_count,
  if(request_count = 0, 0.0, round(toFloat64(fallback_count) / toFloat64(request_count) * 100, 2)) AS fallback_rate,
  if(request_count = 0, 0.0, sum_latency_ms / toFloat64(request_count)) AS avg_latency_ms,
  primary_success,
  fallback_success,
  total_failures,
  updated_at
FROM mv_lb_hourly FINAL;

DROP VIEW IF EXISTS api_key_usage_view;

CREATE VIEW IF NOT EXISTS api_key_usage_view AS
SELECT
  tenant_id,
  api_key_hash,
  user_id,
  period,
  request_count,
  tokens_used,
  finalizeAggregation(unique_sessions_state) AS unique_sessions,
  if(request_count = 0, 0.0, sum_latency_ms / toFloat64(request_count)) AS avg_latency_ms,
  if(request_count = 0, 0.0, round(toFloat64(error_count) / toFloat64(request_count) * 100, 2)) AS error_rate,
  last_activity_at,
  updated_at
FROM mv_api_key_usage_hourly FINAL;

DROP VIEW IF EXISTS chat_sessions_view;

CREATE VIEW IF NOT EXISTS chat_sessions_view AS
SELECT
  tenant_id,
  id,
  user_id,
  api_key,
  model,
  provider,
  message_count,
  tokens_used,
  started_at,
  completed_at,
  duration,
  success,
  error_code,
  error_message,
  metadata,
  correlation_id
FROM chat_sessions;
