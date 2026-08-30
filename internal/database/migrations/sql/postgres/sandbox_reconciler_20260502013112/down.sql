DROP TRIGGER IF EXISTS sandbox_instances_lifecycle_notify ON sandbox_instances;
DROP FUNCTION IF EXISTS notify_sandbox_lifecycle_event();
DROP INDEX IF EXISTS idx_sandbox_instances_reconcile_due;

ALTER TABLE sandbox_instances
    DROP COLUMN IF EXISTS agent_target,
    DROP COLUMN IF EXISTS reconcile_locked_at,
    DROP COLUMN IF EXISTS reconcile_locked_by,
    DROP COLUMN IF EXISTS reconcile_attempts,
    DROP COLUMN IF EXISTS reconcile_after,
    DROP COLUMN IF EXISTS desired_state;
