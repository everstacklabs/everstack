DROP INDEX IF EXISTS idx_sandbox_instances_open_billing_window;

ALTER TABLE sandbox_instances
    DROP COLUMN IF EXISTS billing_ended_at,
    DROP COLUMN IF EXISTS billing_started_at;
