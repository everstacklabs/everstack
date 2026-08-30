DROP INDEX IF EXISTS idx_agent_sessions_trooper_id;
ALTER TABLE agent_sessions DROP COLUMN IF EXISTS trooper_id;
