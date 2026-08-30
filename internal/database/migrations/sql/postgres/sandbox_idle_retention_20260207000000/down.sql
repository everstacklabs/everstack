DROP INDEX IF EXISTS idx_sandbox_instances_last_used;

ALTER TABLE sandbox_instances
    DROP COLUMN IF EXISTS last_used_at,
    DROP COLUMN IF EXISTS idle_retention_secs;
