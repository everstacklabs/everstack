-- Device Signing Keys table for self-hosted gateway M2M authentication
-- This table stores M2M signing keys synced from self-hosted gateways.
-- 
-- Why a separate table?
-- 1. Trial/pre-activation gateways don't have an instance_state record
-- 2. Multiple devices can exist before activations
-- 3. Signing keys can be rotated independently of instance lifecycle
-- 4. Clean separation of concerns: instance_states for activated instances,
--    device_signing_keys for M2M authentication

CREATE TABLE IF NOT EXISTS device_signing_keys (
    device_fingerprint_hash VARCHAR(64) PRIMARY KEY,  -- SHA256 hash of device fingerprint
    signing_key TEXT NOT NULL,                         -- Base64-encoded M2M signing key (32 bytes)
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Index for efficient lookup during M2M token validation
CREATE INDEX IF NOT EXISTS idx_device_signing_keys_updated 
    ON device_signing_keys(updated_at);

COMMENT ON TABLE device_signing_keys IS 'M2M signing keys synced from self-hosted gateways for authentication';
COMMENT ON COLUMN device_signing_keys.device_fingerprint_hash IS 'SHA256 hash of device fingerprint (unique per machine)';
COMMENT ON COLUMN device_signing_keys.signing_key IS 'Base64-encoded M2M signing key (32 bytes), synced from gateway on startup';
