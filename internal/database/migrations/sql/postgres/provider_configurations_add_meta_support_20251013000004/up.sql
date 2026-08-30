-- Add meta-provider support columns to provider_configurations table

-- Add provider_type enum
CREATE TYPE provider_type AS ENUM ('static', 'meta');

ALTER TABLE provider_configurations
    ADD COLUMN IF NOT EXISTS provider_type provider_type DEFAULT 'static' NOT NULL;

-- Add supports_model_discovery column
ALTER TABLE provider_configurations
    ADD COLUMN IF NOT EXISTS supports_model_discovery BOOLEAN DEFAULT false NOT NULL;

-- Add discovery_api_endpoint for meta-providers
ALTER TABLE provider_configurations
    ADD COLUMN IF NOT EXISTS discovery_api_endpoint TEXT;

-- Create index on provider_type for filtering
CREATE INDEX IF NOT EXISTS idx_provider_configurations_provider_type 
    ON provider_configurations(provider_type);

-- Add comments
COMMENT ON COLUMN provider_configurations.provider_type IS 'Type of provider: static (fixed catalog) or meta (dynamic model discovery)';
COMMENT ON COLUMN provider_configurations.supports_model_discovery IS 'Whether the provider supports dynamic model discovery via API';
COMMENT ON COLUMN provider_configurations.discovery_api_endpoint IS 'API endpoint for discovering available models (for meta-providers)';


