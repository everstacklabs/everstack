DROP INDEX IF EXISTS idx_sandbox_instances_recoverable;

ALTER TABLE sandbox_instances
    DROP COLUMN IF EXISTS recovery_attempts;
