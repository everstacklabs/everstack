DROP INDEX IF EXISTS idx_agent_branches_parent_attempt;

ALTER TABLE agent_branches
    DROP CONSTRAINT IF EXISTS fk_agent_branches_parent_attempt;

ALTER TABLE agent_branches
    DROP COLUMN IF EXISTS parent_attempt_id;
