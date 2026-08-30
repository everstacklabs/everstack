DROP TABLE IF EXISTS eval_schedules;
DROP INDEX IF EXISTS idx_eval_runs_baseline;
ALTER TABLE eval_runs DROP COLUMN IF EXISTS regression_result;
ALTER TABLE eval_runs DROP COLUMN IF EXISTS baseline_run_id;
ALTER TABLE eval_runs DROP COLUMN IF EXISTS is_baseline;
