-- Rollback custom_models table

-- Drop foreign key constraint
ALTER TABLE custom_models
    DROP CONSTRAINT IF EXISTS fk_custom_models_provider;

-- Drop indexes
DROP INDEX IF EXISTS idx_custom_models_provider_active;
DROP INDEX IF EXISTS idx_custom_models_is_active;
DROP INDEX IF EXISTS idx_custom_models_provider_name;

-- Drop table
DROP TABLE IF EXISTS custom_models;

-- Drop enum type
DROP TYPE IF EXISTS model_source;

