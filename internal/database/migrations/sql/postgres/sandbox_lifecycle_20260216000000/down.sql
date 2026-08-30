DROP INDEX IF EXISTS idx_sandbox_lifecycle_revivable;

ALTER TABLE sandbox_instances
    DROP COLUMN IF EXISTS lifecycle_state,
    DROP COLUMN IF EXISTS workspace_snapshot_ref,
    DROP COLUMN IF EXISTS revivable_until,
    DROP COLUMN IF EXISTS stopped_at,
    DROP COLUMN IF EXISTS updated_at;
