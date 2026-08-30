-- Create provider_api_keys table for managing multiple API keys per provider
CREATE TABLE IF NOT EXISTS provider_api_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_config_id UUID NOT NULL REFERENCES provider_configurations(id) ON DELETE CASCADE,
    key_name TEXT NOT NULL,
    key_encrypted TEXT NOT NULL,
    weight INT NOT NULL DEFAULT 1,
    is_active BOOLEAN NOT NULL DEFAULT true,
    rate_limit_tracking JSONB DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(provider_config_id, key_name)
);

-- Create indexes for efficient querying
CREATE INDEX idx_provider_api_keys_config_id ON provider_api_keys(provider_config_id);
CREATE INDEX idx_provider_api_keys_active ON provider_api_keys(provider_config_id, is_active);

-- Add comment for documentation
COMMENT ON TABLE provider_api_keys IS 'Stores multiple API keys per provider with weights for load balancing';
COMMENT ON COLUMN provider_api_keys.weight IS 'Weight for weighted load balancing (higher weight = more traffic)';
COMMENT ON COLUMN provider_api_keys.rate_limit_tracking IS 'JSONB field for storing rate limit state per key';

