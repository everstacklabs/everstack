-- Create views for read models (Postgres)

CREATE OR REPLACE VIEW chat_sessions_view AS
SELECT 
    CAST(payload->>'session_id' AS TEXT) as id,
    CAST(payload->>'user_id' AS TEXT) as user_id,
    CAST(payload->>'api_key' AS TEXT) as api_key,
    CAST(payload->>'model' AS TEXT) as model,
    CAST(payload->>'provider' AS TEXT) as provider,
    CAST(payload->>'message_count' AS INT) as message_count,
    CAST(payload->>'tokens_used' AS INT) as tokens_used,
    TO_TIMESTAMP(CAST(payload->>'started_at' AS TEXT), 'YYYY-MM-DD"T"HH24:MI:SS"Z"') as started_at,
    CASE 
        WHEN payload->>'completed_at' IS NOT NULL 
        THEN TO_TIMESTAMP(CAST(payload->>'completed_at' AS TEXT), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
        ELSE NULL 
    END as completed_at,
    CAST(payload->>'duration_ms' AS BIGINT) as duration,
    CAST(payload->>'success' AS BOOLEAN) as success,
    CAST(payload->>'error_code' AS TEXT) as error_code,
    CAST(payload->>'error_message' AS TEXT) as error_message,
    payload->'metadata' as metadata,
    CAST(payload->>'correlation_id' AS TEXT) as correlation_id
FROM events 
WHERE type = 'chat.session.started';

CREATE OR REPLACE VIEW model_usage_stats_view AS
SELECT 
    CAST(payload->>'provider' AS TEXT) as provider,
    CAST(payload->>'model' AS TEXT) as model,
    DATE_TRUNC('hour', TO_TIMESTAMP(created_at)) as period,
    COUNT(*) as request_count,
    COUNT(CASE WHEN payload->>'success' = 'true' THEN 1 END) as success_count,
    COUNT(CASE WHEN payload->>'success' = 'false' THEN 1 END) as error_count,
    AVG(CAST(payload->>'latency_ms' AS NUMERIC)) as avg_latency_ms,
    MIN(CAST(payload->>'latency_ms' AS NUMERIC)) as min_latency_ms,
    MAX(CAST(payload->>'latency_ms' AS NUMERIC)) as max_latency_ms,
    SUM(CAST(payload->>'tokens_used' AS BIGINT)) as total_tokens_used,
    SUM(CAST(payload->>'cost' AS NUMERIC)) as total_cost,
    MAX(TO_TIMESTAMP(created_at)) as updated_at
FROM events 
WHERE type IN ('chat.session.started', 'embedding.request.completed')
GROUP BY 
    CAST(payload->>'provider' AS TEXT),
    CAST(payload->>'model' AS TEXT),
    DATE_TRUNC('hour', TO_TIMESTAMP(created_at));

CREATE OR REPLACE VIEW load_balancer_stats_view AS
SELECT 
    DATE_TRUNC('hour', TO_TIMESTAMP(created_at)) as period,
    CAST(payload->>'strategy' AS TEXT) as strategy,
    CAST(payload->>'key_source' AS TEXT) as key_source,
    COUNT(*) as request_count,
    COUNT(CASE WHEN payload->>'fallback_used' = 'true' THEN 1 END) as fallback_count,
    ROUND(
        COUNT(CASE WHEN payload->>'fallback_used' = 'true' THEN 1 END)::NUMERIC / 
        COUNT(*)::NUMERIC * 100, 2
    ) as fallback_rate,
    AVG(CAST(payload->>'latency_ms' AS NUMERIC)) as avg_latency_ms,
    COUNT(CASE WHEN payload->>'primary_success' = 'true' THEN 1 END) as primary_success,
    COUNT(CASE WHEN payload->>'fallback_success' = 'true' THEN 1 END) as fallback_success,
    COUNT(CASE WHEN payload->>'success' = 'false' THEN 1 END) as total_failures,
    MAX(TO_TIMESTAMP(created_at)) as updated_at
FROM events 
WHERE type = 'load_balancer.request.completed'
GROUP BY 
    DATE_TRUNC('hour', TO_TIMESTAMP(created_at)),
    CAST(payload->>'strategy' AS TEXT),
    CAST(payload->>'key_source' AS TEXT);

CREATE OR REPLACE VIEW error_rates_view AS
SELECT 
    CAST(payload->>'provider' AS TEXT) as provider,
    CAST(payload->>'model' AS TEXT) as model,
    CAST(payload->>'error_type' AS TEXT) as error_type,
    CAST(payload->>'error_code' AS TEXT) as error_code,
    DATE_TRUNC('hour', TO_TIMESTAMP(created_at)) as period,
    COUNT(*) as error_count,
    -- Total count needs to be calculated separately
    0 as total_count,
    0.0 as error_rate,
    MAX(TO_TIMESTAMP(created_at)) as updated_at
FROM events 
WHERE type IN ('chat.session.error', 'embedding.request.error')
GROUP BY 
    CAST(payload->>'provider' AS TEXT),
    CAST(payload->>'model' AS TEXT),
    CAST(payload->>'error_type' AS TEXT),
    CAST(payload->>'error_code' AS TEXT),
    DATE_TRUNC('hour', TO_TIMESTAMP(created_at));

CREATE OR REPLACE VIEW model_configs_view AS
SELECT DISTINCT ON (provider, model_id)
    CAST(payload->>'provider' AS TEXT) as provider,
    CAST(payload->>'model_id' AS TEXT) as model_id,
    CAST(payload->>'alias' AS TEXT) as alias,
    payload->'config' as config,
    CAST(payload->>'enabled' AS BOOLEAN) as enabled,
    CAST(payload->>'is_default' AS BOOLEAN) as is_default,
    TO_TIMESTAMP(created_at) as created_at,
    TO_TIMESTAMP(created_at) as updated_at,
    CAST(payload->>'version' AS INT) as version
FROM events 
WHERE type = 'model.config.changed'
ORDER BY provider, model_id, created_at DESC;

CREATE OR REPLACE VIEW api_key_usage_view AS
SELECT 
    CAST(payload->>'api_key_hash' AS TEXT) as api_key_hash,
    CAST(payload->>'user_id' AS TEXT) as user_id,
    DATE_TRUNC('hour', TO_TIMESTAMP(created_at)) as period,
    COUNT(*) as request_count,
    SUM(CAST(payload->>'tokens_used' AS BIGINT)) as tokens_used,
    COUNT(DISTINCT CAST(payload->>'session_id' AS TEXT)) as unique_sessions,
    AVG(CAST(payload->>'latency_ms' AS NUMERIC)) as avg_latency_ms,
    COUNT(CASE WHEN payload->>'success' = 'false' THEN 1 END)::FLOAT / COUNT(*)::FLOAT * 100 as error_rate,
    MAX(TO_TIMESTAMP(created_at)) as last_activity_at,
    MAX(TO_TIMESTAMP(created_at)) as updated_at
FROM events 
WHERE type IN ('chat.session.started', 'embedding.request.started')
  AND payload->>'api_key_hash' IS NOT NULL
GROUP BY 
    CAST(payload->>'api_key_hash' AS TEXT),
    CAST(payload->>'user_id' AS TEXT),
    DATE_TRUNC('hour', TO_TIMESTAMP(created_at));