-- Create runtime_config table for storing runtime-modifiable gateway configuration
-- This table stores configuration sections that can be updated without gateway restart

CREATE TABLE IF NOT EXISTS runtime_config (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    section TEXT NOT NULL UNIQUE,  -- 'rate_limit', 'load_balancer', 'features', 'cache', 'telemetry', 'cors'
    config JSONB NOT NULL DEFAULT '{}'::jsonb,
    version INT NOT NULL DEFAULT 1,
    updated_by UUID,  -- Reference to user who last updated
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Create index on section for fast lookups
CREATE INDEX IF NOT EXISTS idx_runtime_config_section 
    ON runtime_config(section);

-- Add comments for documentation
COMMENT ON TABLE runtime_config IS 'Stores runtime-modifiable gateway configuration sections';
COMMENT ON COLUMN runtime_config.section IS 'Configuration section name: rate_limit, load_balancer, features, cache, telemetry, cors';
COMMENT ON COLUMN runtime_config.config IS 'JSONB configuration data for the section';
COMMENT ON COLUMN runtime_config.version IS 'Version counter for optimistic locking';
COMMENT ON COLUMN runtime_config.updated_by IS 'UUID of user who last updated this section';

-- Seed with default configuration sections
-- These defaults match gateway-default.yaml runtime-modifiable sections

INSERT INTO runtime_config (section, config, version) VALUES
(
    'rate_limit',
    '{
        "enabled": true,
        "requests_per_minute": 500,
        "burst": 100,
        "key_source": "correlation"
    }'::jsonb,
    1
),
(
    'load_balancer',
    '{
        "enabled": true,
        "strategy": "round_robin",
        "key_source": "correlation"
    }'::jsonb,
    1
),
(
    'features',
    '{
        "enable_streaming": true,
        "enable_embeddings": true,
        "enable_function_calling": true,
        "enable_response_caching": true,
        "enable_sse": false,
        "enable_request_logging": true,
        "enable_health_checks": true,
        "enable_agents": false
    }'::jsonb,
    1
),
(
    'cache',
    '{
        "enabled": true,
        "type": "memory",
        "ttl": "10m",
        "memory_max_size": 50000,
        "redis_address": "",
        "redis_db": 0,
        "redis_pool_size": 100
    }'::jsonb,
    1
),
(
    'telemetry',
    '{
        "enabled": false,
        "sampling_rate": 1.0,
        "granularity": "standard",
        "trace_provider_calls": true,
        "trace_stream_chunks": false,
        "trace_fallbacks": true,
        "collector_url": "localhost:4317",
        "service_name": "everstack-gateway"
    }'::jsonb,
    1
),
(
    'cors',
    '{
        "enabled": true,
        "allowed_origins": ["*"],
        "allowed_methods": ["GET", "POST", "PUT", "DELETE", "OPTIONS"],
        "allowed_headers": ["*"],
        "exposed_headers": [],
        "allow_credentials": true,
        "max_age": ""
    }'::jsonb,
    1
)
ON CONFLICT (section) DO NOTHING;
