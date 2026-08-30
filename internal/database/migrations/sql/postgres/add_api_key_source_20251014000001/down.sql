-- Remove api_key_source column
ALTER TABLE provider_configurations
DROP COLUMN api_key_source;
