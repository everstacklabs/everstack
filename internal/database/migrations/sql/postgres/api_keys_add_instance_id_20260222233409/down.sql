DROP INDEX IF EXISTS idx_api_keys_instance_id;
ALTER TABLE api_keys DROP COLUMN IF EXISTS instance_id;
