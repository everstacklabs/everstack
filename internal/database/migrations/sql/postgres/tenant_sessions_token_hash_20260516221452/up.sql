-- Promote cloud_relay_sessions from a shadow/audit table into the source of
-- truth for instance authentication. Before this change the instance cookie
-- held the user's cloud session token and tenant middleware validated against
-- everstack.sessions on every request, which bridged cloud and instance
-- lifecycles and let parent-domain cookies leak identity across instances.
--
-- After this change the instance cookie holds a fresh, instance-bound token
-- whose SHA-256 lives in token_hash here. cloud_session_id is kept for audit
-- lineage only and becomes nullable.
ALTER TABLE cloud_relay_sessions
    ADD COLUMN IF NOT EXISTS token_hash BYTEA,
    ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;

ALTER TABLE cloud_relay_sessions
    ALTER COLUMN cloud_session_id DROP NOT NULL;

-- Partial unique index: legacy rows have NULL token_hash and aren't lookup
-- targets. New rows must collide-free on their hash.
CREATE UNIQUE INDEX IF NOT EXISTS idx_cloud_relay_sessions_token_hash
    ON cloud_relay_sessions (token_hash)
    WHERE token_hash IS NOT NULL;

-- Hot path for validateTenantSession: latest active row per token.
CREATE INDEX IF NOT EXISTS idx_cloud_relay_sessions_active_by_user
    ON cloud_relay_sessions (cloud_user_id)
    WHERE ended_at IS NULL AND token_hash IS NOT NULL;
