-- Auto-recovery bookkeeping for sandboxes that died while the user wanted
-- them running.
--
-- When the HealthSweeper confirms a running VM is gone it flips the row to
-- lifecycle_state='error' (error_reason='vm_not_found') while PRESERVING
-- desired_state. Before this change nothing ever re-converged such a row:
-- it parked in 'error' for 365 days awaiting a manual Recover(). Rows were
-- observed stuck in error/desired=running for 16+ days in production.
--
-- recovery_attempts counts consecutive auto-recovery attempts that have not
-- yet produced a successful convergence. It is DISTINCT from
-- reconcile_attempts: reconcile_attempts is the per-convergence retry budget
-- (reset to 0 on every successful transition, including the error→reviving
-- recovery hop), so it cannot survive the error→reviving→error cycle and
-- therefore cannot bound the recovery loop. recovery_attempts survives that
-- cycle and is reset to 0 only when the row reaches a stable state again.
ALTER TABLE sandbox_instances
    ADD COLUMN IF NOT EXISTS recovery_attempts INT NOT NULL DEFAULT 0;

-- Partial index for the RecoveryChecker's eligibility scan: only error rows
-- are ever candidates, and the scan orders by error_at to honour backoff.
CREATE INDEX IF NOT EXISTS idx_sandbox_instances_recoverable
    ON sandbox_instances (error_at)
    WHERE lifecycle_state = 'error';
