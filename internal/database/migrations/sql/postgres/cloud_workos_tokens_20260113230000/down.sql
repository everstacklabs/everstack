-- Remove WorkOS OAuth token columns from sessions table

DROP INDEX IF EXISTS idx_sessions_workos_token_expires;

ALTER TABLE sessions
    DROP COLUMN IF EXISTS workos_access_token,
    DROP COLUMN IF EXISTS workos_refresh_token,
    DROP COLUMN IF EXISTS workos_token_expires_at,
    DROP COLUMN IF EXISTS workos_organization_id,
    DROP COLUMN IF EXISTS last_refresh_attempt_at,
    DROP COLUMN IF EXISTS refresh_failure_count;
