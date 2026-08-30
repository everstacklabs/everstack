ALTER TABLE sandbox_instances
    ADD COLUMN IF NOT EXISTS lifecycle_state        VARCHAR(50) NOT NULL DEFAULT 'running',
    ADD COLUMN IF NOT EXISTS workspace_snapshot_ref  TEXT,
    ADD COLUMN IF NOT EXISTS revivable_until         TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS stopped_at              TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW();

CREATE INDEX IF NOT EXISTS idx_sandbox_lifecycle_revivable
    ON sandbox_instances (lifecycle_state, revivable_until)
    WHERE lifecycle_state = 'stopped';
