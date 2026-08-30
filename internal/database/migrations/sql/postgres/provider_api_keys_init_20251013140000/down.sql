-- Drop indexes
DROP INDEX IF EXISTS idx_provider_api_keys_active;
DROP INDEX IF EXISTS idx_provider_api_keys_config_id;

-- Drop provider_api_keys table
DROP TABLE IF EXISTS provider_api_keys;

