-- Rollback must not turn bot credentials into unrestricted API keys.
UPDATE api_keys SET revoked = TRUE, revoked_at = NOW(), updated_at = NOW()
WHERE surfaces IS NOT NULL AND NOT revoked;
DROP TABLE IF EXISTS bot_connections;
ALTER TABLE api_keys DROP COLUMN IF EXISTS last_used_at;
ALTER TABLE api_keys DROP COLUMN IF EXISTS surfaces;
