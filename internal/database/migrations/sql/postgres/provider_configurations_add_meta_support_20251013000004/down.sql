-- Rollback meta-provider support columns from provider_configurations table

-- Drop index
DROP INDEX IF EXISTS idx_provider_configurations_provider_type;

-- Drop columns
ALTER TABLE provider_configurations
    DROP COLUMN IF EXISTS discovery_api_endpoint;

ALTER TABLE provider_configurations
    DROP COLUMN IF EXISTS supports_model_discovery;

ALTER TABLE provider_configurations
    DROP COLUMN IF EXISTS provider_type;

-- Drop enum type (only if no other tables use it)
DROP TYPE IF EXISTS provider_type;

