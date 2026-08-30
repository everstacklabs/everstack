ALTER TABLE eval_run_items DROP COLUMN IF EXISTS hypothesis_diff;
ALTER TABLE eval_run_items DROP COLUMN IF EXISTS hypothesis_text;
ALTER TABLE agent_branches DROP COLUMN IF EXISTS hypothesis_diff;
ALTER TABLE agent_branches DROP COLUMN IF EXISTS hypothesis_text;
