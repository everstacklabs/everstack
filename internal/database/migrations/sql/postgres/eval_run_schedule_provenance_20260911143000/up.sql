ALTER TABLE eval_runs
    ADD COLUMN IF NOT EXISTS schedule_id TEXT;

CREATE INDEX IF NOT EXISTS idx_eval_runs_tenant_schedule_created
    ON eval_runs (tenant_id, schedule_id, created_at DESC)
    WHERE schedule_id IS NOT NULL;
