-- Allow agent_sessions to be associated with a trooper instead of an agent
ALTER TABLE agent_sessions ALTER COLUMN agent_id DROP NOT NULL;
ALTER TABLE agent_sessions ADD COLUMN IF NOT EXISTS trooper_id UUID REFERENCES troopers(id);
CREATE INDEX IF NOT EXISTS idx_agent_sessions_trooper_id ON agent_sessions(trooper_id) WHERE trooper_id IS NOT NULL;
