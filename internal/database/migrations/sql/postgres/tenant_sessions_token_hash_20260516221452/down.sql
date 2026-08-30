DROP INDEX IF EXISTS idx_cloud_relay_sessions_active_by_user;
DROP INDEX IF EXISTS idx_cloud_relay_sessions_token_hash;

ALTER TABLE cloud_relay_sessions
    ALTER COLUMN cloud_session_id SET NOT NULL;

ALTER TABLE cloud_relay_sessions
    DROP COLUMN IF EXISTS expires_at,
    DROP COLUMN IF EXISTS token_hash;
