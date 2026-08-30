-- Add api_key_source column to track whether API key came from YAML or UI
ALTER TABLE provider_configurations
ADD COLUMN api_key_source VARCHAR(10) DEFAULT 'yaml';

-- Set existing records based on whether they have API keys
UPDATE provider_configurations
SET api_key_source = CASE 
    WHEN api_key_encrypted LIKE '${%' THEN 'yaml'
    WHEN api_key_encrypted != '' THEN 'ui'
    ELSE 'yaml'
END;

-- Add comment to document the column
COMMENT ON COLUMN provider_configurations.api_key_source IS 'Source of the API key: yaml (from config file) or ui (added via admin interface)';
