-- Reverse of the lifecycle unification. The 'sleeping' -> 'stopped'
-- vocabulary rollback is intentionally NOT performed: 'sleeping' was
-- already a valid value before this migration (reconciler path), so
-- rows are left as-is and the legacy readers' stopped-handling keeps
-- working through the lifecycle.ts / API fallbacks.

DROP INDEX IF EXISTS idx_sandbox_instances_error;

DROP INDEX IF EXISTS idx_sandbox_instances_reconcile_due;
CREATE INDEX IF NOT EXISTS idx_sandbox_instances_reconcile_due
    ON sandbox_instances (reconcile_after)
    WHERE lifecycle_state IN ('pending', 'creating', 'stopping', 'reviving', 'terminating');

ALTER TABLE sandbox_instances
    DROP COLUMN IF EXISTS error_reason,
    DROP COLUMN IF EXISTS error_at,
    DROP COLUMN IF EXISTS auto_stop_minutes,
    DROP COLUMN IF EXISTS auto_archive_minutes,
    DROP COLUMN IF EXISTS auto_delete_minutes,
    DROP COLUMN IF EXISTS workspace_archive_ref;
