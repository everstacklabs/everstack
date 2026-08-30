-- Drop provider_model_status table
DROP TABLE IF EXISTS provider_model_status;

-- Remove catalog sync fields from provider_configurations table
ALTER TABLE provider_configurations
DROP COLUMN IF EXISTS catalog_status,
DROP COLUMN IF EXISTS is_from_catalog,
DROP COLUMN IF EXISTS catalog_synced_at,
DROP COLUMN IF EXISTS deprecated_at;

-- Drop indexes (will be automatically dropped with columns, but explicit for clarity)
DROP INDEX IF EXISTS idx_provider_configurations_catalog_status;
DROP INDEX IF EXISTS idx_provider_configurations_is_from_catalog;

