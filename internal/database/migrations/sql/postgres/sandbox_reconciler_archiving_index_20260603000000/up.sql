DROP INDEX IF EXISTS idx_sandbox_instances_reconcile_due;

CREATE INDEX idx_sandbox_instances_reconcile_due
    ON sandbox_instances (reconcile_after)
    WHERE lifecycle_state IN ('pending', 'creating', 'stopping', 'reviving', 'archiving', 'terminating');
