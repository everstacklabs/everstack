-- Add source column to track origin of API key
ALTER TABLE provider_api_keys 
ADD COLUMN source VARCHAR(20) NOT NULL DEFAULT 'manual';

-- Add check constraint to ensure valid values
ALTER TABLE provider_api_keys
ADD CONSTRAINT provider_api_keys_source_check 
CHECK (source IN ('manual', 'config'));

-- Add comment
COMMENT ON COLUMN provider_api_keys.source IS 'Source of API key: manual (user-added) or config (from YAML)';

-- Create index for efficient filtering
CREATE INDEX idx_provider_api_keys_source ON provider_api_keys(provider_config_id, source);

