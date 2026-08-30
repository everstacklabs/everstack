-- Extend agent_tasks table with async spawn columns
ALTER TABLE agent_tasks ADD COLUMN IF NOT EXISTS session_id VARCHAR(255);
ALTER TABLE agent_tasks ADD COLUMN IF NOT EXISTS spawn_tree_id UUID;
ALTER TABLE agent_tasks ADD COLUMN IF NOT EXISTS agent_id VARCHAR(255);
ALTER TABLE agent_tasks ADD COLUMN IF NOT EXISTS prompt_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agent_tasks ADD COLUMN IF NOT EXISTS completion_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agent_tasks ADD COLUMN IF NOT EXISTS total_tokens INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_agent_tasks_session ON agent_tasks (session_id) WHERE session_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_tasks_spawn_tree ON agent_tasks (spawn_tree_id) WHERE spawn_tree_id IS NOT NULL;
