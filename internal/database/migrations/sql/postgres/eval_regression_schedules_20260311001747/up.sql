-- Add baseline and regression columns to eval_runs
ALTER TABLE eval_runs ADD COLUMN IF NOT EXISTS is_baseline BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE eval_runs ADD COLUMN IF NOT EXISTS baseline_run_id TEXT;
ALTER TABLE eval_runs ADD COLUMN IF NOT EXISTS regression_result JSONB;

-- Index for quickly finding the baseline run for a dataset+target combo
CREATE INDEX IF NOT EXISTS idx_eval_runs_baseline
    ON eval_runs (tenant_id, dataset_id, eval_target_type, eval_target_id)
    WHERE is_baseline = TRUE;

-- Eval schedules table
CREATE TABLE IF NOT EXISTS eval_schedules (
    id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    tenant_id       TEXT NOT NULL,
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    dataset_id      TEXT NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
    eval_target_type TEXT NOT NULL,
    eval_target_id  TEXT NOT NULL DEFAULT '',
    eval_config     JSONB NOT NULL DEFAULT '{}',
    scorer_config_ids TEXT[] NOT NULL DEFAULT '{}',
    cron_expression TEXT NOT NULL,
    timezone        TEXT NOT NULL DEFAULT 'UTC',
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    last_run_at     TIMESTAMPTZ,
    next_run_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, name)
);

CREATE INDEX IF NOT EXISTS idx_eval_schedules_tenant ON eval_schedules (tenant_id);
CREATE INDEX IF NOT EXISTS idx_eval_schedules_next_run ON eval_schedules (next_run_at) WHERE enabled = TRUE;
