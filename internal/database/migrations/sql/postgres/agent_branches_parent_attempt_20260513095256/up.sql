-- Phase 3a: attempt lineage on agent_branches.
--
-- Adds parent_attempt_id so retry/fork chains are explicit (today they
-- group by batch_id/session_id but have no "this attempt was a child of
-- that failed attempt" relationship). Used by verdict-rate dashboards
-- to compute "for the same problem, the model+prompt combination that
-- finally won" and by Phase 3d CI gating to surface which attempts
-- flipped from win → fail or fail → win across a baseline.
--
-- Nullable because (a) the first attempt has no parent, and (b)
-- pre-existing rows must continue to insert without backfill.

ALTER TABLE agent_branches
    ADD COLUMN IF NOT EXISTS parent_attempt_id UUID;

-- Self-FK so cascade behavior is deterministic. ON DELETE SET NULL
-- rather than CASCADE — losing a parent should not erase children that
-- carry their own labeled verdicts.
ALTER TABLE agent_branches
    ADD CONSTRAINT fk_agent_branches_parent_attempt
    FOREIGN KEY (parent_attempt_id)
    REFERENCES agent_branches(id)
    ON DELETE SET NULL;

-- Cheap index — typical query pattern is "find all children of attempt X"
-- when rendering a verdict heatmap or tracing a fix lineage.
CREATE INDEX IF NOT EXISTS idx_agent_branches_parent_attempt
    ON agent_branches (parent_attempt_id)
    WHERE parent_attempt_id IS NOT NULL;
