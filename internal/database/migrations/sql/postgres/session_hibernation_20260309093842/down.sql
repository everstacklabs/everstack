DROP INDEX IF EXISTS idx_sessions_hibernation;

ALTER TABLE agent_sessions
    DROP COLUMN IF EXISTS pending_steers,
    DROP COLUMN IF EXISTS hibernated_at;
