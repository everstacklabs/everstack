-- Add WorkOS OAuth tokens to sessions table for cloud authentication
-- This enables automatic token refresh when access tokens expire

-- Add columns for WorkOS tokens
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS workos_access_token TEXT,
    ADD COLUMN IF NOT EXISTS workos_refresh_token TEXT,
    ADD COLUMN IF NOT EXISTS workos_token_expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS workos_organization_id VARCHAR(255);

-- Add columns for refresh backoff tracking
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS last_refresh_attempt_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS refresh_failure_count INTEGER NOT NULL DEFAULT 0;

-- Add index for token expiration queries (for background refresh jobs)
CREATE INDEX IF NOT EXISTS idx_sessions_workos_token_expires 
    ON sessions(workos_token_expires_at) 
    WHERE workos_token_expires_at IS NOT NULL;

COMMENT ON COLUMN sessions.workos_access_token IS 'WorkOS access token for API calls';
COMMENT ON COLUMN sessions.workos_refresh_token IS 'WorkOS refresh token for obtaining new access tokens';
COMMENT ON COLUMN sessions.workos_token_expires_at IS 'When the WorkOS access token expires';
COMMENT ON COLUMN sessions.workos_organization_id IS 'WorkOS organization ID the user authenticated with';
COMMENT ON COLUMN sessions.last_refresh_attempt_at IS 'When we last attempted to refresh the WorkOS token (for backoff)';
COMMENT ON COLUMN sessions.refresh_failure_count IS 'Consecutive token refresh failures (0 = success, used for exponential backoff)';