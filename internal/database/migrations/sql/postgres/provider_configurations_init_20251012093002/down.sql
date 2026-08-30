-- Drop indexes
DROP INDEX IF EXISTS idx_provider_configurations_is_active;
DROP INDEX IF EXISTS idx_provider_configurations_provider_name;

-- Drop table
DROP TABLE IF EXISTS provider_configurations;

