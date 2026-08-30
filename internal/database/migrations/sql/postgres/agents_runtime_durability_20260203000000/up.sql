ALTER TABLE agent_sessions
  ADD COLUMN IF NOT EXISTS instance_id VARCHAR(255),
  ADD COLUMN IF NOT EXISTS heartbeat_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_agent_sessions_instance_heartbeat
  ON agent_sessions(instance_id, heartbeat_at)
  WHERE status IN ('running', 'waiting_for_approval');
