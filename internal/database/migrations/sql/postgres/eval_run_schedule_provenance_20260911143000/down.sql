DROP INDEX IF EXISTS idx_eval_runs_tenant_schedule_created;

ALTER TABLE eval_runs
    DROP COLUMN IF EXISTS schedule_id;
