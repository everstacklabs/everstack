-- M2M Authentication: Add signing_key column to instances table (local gateway database)
-- This column stores the HMAC signing key received during activation for M2M request authentication.

ALTER TABLE system.instances ADD COLUMN IF NOT EXISTS signing_key TEXT;

COMMENT ON COLUMN system.instances.signing_key IS 'HMAC signing key for M2M authentication (base64 encoded, received from license service)';


