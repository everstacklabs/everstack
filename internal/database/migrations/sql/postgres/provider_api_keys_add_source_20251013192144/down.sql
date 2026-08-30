DROP INDEX IF EXISTS idx_provider_api_keys_source;
ALTER TABLE provider_api_keys DROP CONSTRAINT IF EXISTS provider_api_keys_source_check;
ALTER TABLE provider_api_keys DROP COLUMN IF EXISTS source;

