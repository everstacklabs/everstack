ALTER TABLE sandbox_instances
    ADD COLUMN IF NOT EXISTS last_used_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS idle_retention_secs INT;

-- Backfill: treat creation time as last use for existing rows
UPDATE sandbox_instances
SET last_used_at = created_at
WHERE last_used_at IS NULL;

-- Index for the reaper: quickly find running sandboxes ordered by last use
CREATE INDEX IF NOT EXISTS idx_sandbox_instances_last_used
    ON sandbox_instances(last_used_at)
    WHERE status IN ('pending', 'running');
