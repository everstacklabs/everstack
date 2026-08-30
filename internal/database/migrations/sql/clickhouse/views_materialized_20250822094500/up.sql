CREATE TABLE IF NOT EXISTS mv_model_usage_hourly
(
  provider String,
  model String,
  period DateTime,
  request_count SimpleAggregateFunction(sum, UInt64),
  success_count SimpleAggregateFunction(sum, UInt64),
  error_count SimpleAggregateFunction(sum, UInt64),
  sum_latency_ms SimpleAggregateFunction(sum, Float64),
  min_latency_ms SimpleAggregateFunction(min, Float64),
  max_latency_ms SimpleAggregateFunction(max, Float64),
  total_tokens_used SimpleAggregateFunction(sum, UInt64),
  total_cost SimpleAggregateFunction(sum, Float64),
  updated_at SimpleAggregateFunction(max, DateTime)
)
ENGINE = AggregatingMergeTree()
ORDER BY (provider, model, period);

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

CREATE VIEW IF NOT EXISTS model_usage_stats_view AS
SELECT
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

-- 2) Load balancer hourly rollup (SimpleAggregateFunction numeric columns)

CREATE TABLE IF NOT EXISTS mv_lb_hourly
(
  period DateTime,
  strategy String,
  key_source String,
  request_count SimpleAggregateFunction(sum, UInt64),
  fallback_count SimpleAggregateFunction(sum, UInt64),
  sum_latency_ms SimpleAggregateFunction(sum, Float64),
  primary_success SimpleAggregateFunction(sum, UInt64),
  fallback_success SimpleAggregateFunction(sum, UInt64),
  total_failures SimpleAggregateFunction(sum, UInt64),
  updated_at SimpleAggregateFunction(max, DateTime)
)
ENGINE = AggregatingMergeTree()
ORDER BY (period, strategy, key_source);

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

CREATE VIEW IF NOT EXISTS load_balancer_stats_view AS
SELECT
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

CREATE TABLE IF NOT EXISTS mv_api_key_usage_hourly
(
  api_key_hash String,
  user_id String,
  period DateTime,
  request_count SimpleAggregateFunction(sum, UInt64),
  tokens_used SimpleAggregateFunction(sum, UInt64),
  sum_latency_ms SimpleAggregateFunction(sum, Float64),
  error_count SimpleAggregateFunction(sum, UInt64),
  last_activity_at SimpleAggregateFunction(max, DateTime),
  updated_at SimpleAggregateFunction(max, DateTime),
  unique_sessions_state AggregateFunction(uniqCombined64, String)
)
ENGINE = AggregatingMergeTree()
ORDER BY (api_key_hash, user_id, period);

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

CREATE VIEW IF NOT EXISTS api_key_usage_view AS
SELECT
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

-- 4) Chat sessions denormalized table + MV + compatibility view

CREATE TABLE IF NOT EXISTS chat_sessions
(
  id String,
  user_id String,
  api_key String,
  model String,
  provider String,
  message_count Int32,
  tokens_used Int32,
  started_at Nullable(DateTime),
  completed_at Nullable(DateTime),
  duration UInt64,
  success UInt8,
  error_code String,
  error_message String,
  metadata String,
  correlation_id String,
  created_at DateTime
)
ENGINE = MergeTree
ORDER BY (id, created_at);

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


CREATE VIEW IF NOT EXISTS chat_sessions_view AS
SELECT
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