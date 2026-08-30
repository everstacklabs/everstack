ALTER TABLE agent_tasks DROP COLUMN IF EXISTS session_id;
ALTER TABLE agent_tasks DROP COLUMN IF EXISTS spawn_tree_id;
ALTER TABLE agent_tasks DROP COLUMN IF EXISTS agent_id;
ALTER TABLE agent_tasks DROP COLUMN IF EXISTS prompt_tokens;
ALTER TABLE agent_tasks DROP COLUMN IF EXISTS completion_tokens;
ALTER TABLE agent_tasks DROP COLUMN IF EXISTS total_tokens;
