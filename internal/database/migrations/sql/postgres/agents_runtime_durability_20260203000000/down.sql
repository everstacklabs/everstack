DROP INDEX IF EXISTS idx_agent_sessions_instance_heartbeat;

ALTER TABLE agent_sessions
  DROP COLUMN IF EXISTS heartbeat_at,
  DROP COLUMN IF EXISTS instance_id;
