-- Add labels column to sandbox_instances for arbitrary key-value metadata.
--
-- Labels let agent orchestrators tag sandboxes by run ID, agent ID, repo,
-- PR number, etc. and filter the list API by label. Setting labels replaces
-- the full set (PATCH semantics). GIN index enables efficient @> containment
-- queries for label-based filtering.

ALTER TABLE sandbox_instances
    ADD COLUMN IF NOT EXISTS labels JSONB NOT NULL DEFAULT '{}';

-- GIN index for fast ?label[key]=value containment queries.
CREATE INDEX IF NOT EXISTS idx_sandbox_instances_labels_gin
    ON sandbox_instances USING GIN (labels);
