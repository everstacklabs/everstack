-- Create provider_configurations table for storing LLM provider configurations
CREATE TABLE IF NOT EXISTS provider_configurations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_name TEXT NOT NULL UNIQUE,
    api_key_encrypted TEXT NOT NULL,
    enabled_models JSONB NOT NULL DEFAULT '[]'::jsonb,
    custom_base_url TEXT,
    custom_settings JSONB DEFAULT '{}'::jsonb,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    synced_to_yaml_at TIMESTAMPTZ
);

-- Create index on provider_name for faster lookups
CREATE INDEX IF NOT EXISTS idx_provider_configurations_provider_name 
    ON provider_configurations(provider_name);

-- Create index on is_active for filtering active providers
CREATE INDEX IF NOT EXISTS idx_provider_configurations_is_active 
    ON provider_configurations(is_active);

-- Note: updated_at is handled in the application code (repository layer)

-- Add comments to table
COMMENT ON TABLE provider_configurations IS 'Stores user configurations for LLM providers (OpenAI, Anthropic, etc.)';
COMMENT ON COLUMN provider_configurations.api_key_encrypted IS 'Encrypted API key for the provider';
COMMENT ON COLUMN provider_configurations.enabled_models IS 'Array of model names that are enabled for this provider';
COMMENT ON COLUMN provider_configurations.custom_base_url IS 'Optional custom base URL to override the default provider URL';
COMMENT ON COLUMN provider_configurations.custom_settings IS 'Additional provider-specific configuration settings';
COMMENT ON COLUMN provider_configurations.synced_to_yaml_at IS 'Timestamp of last sync to gateway.yaml file';

