ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS instance_id TEXT;
CREATE INDEX IF NOT EXISTS idx_api_keys_instance_id ON api_keys (instance_id) WHERE instance_id IS NOT NULL;
