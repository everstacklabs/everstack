ALTER TABLE agent_sessions
    ADD COLUMN IF NOT EXISTS hibernated_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS pending_steers JSONB DEFAULT '[]';

CREATE INDEX IF NOT EXISTS idx_sessions_hibernation
    ON agent_sessions(updated_at)
    WHERE status = 'waiting_for_input' AND hibernated_at IS NULL;
