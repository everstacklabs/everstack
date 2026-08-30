DROP INDEX IF EXISTS uq_eval_run_items_run_dataset_item;
DROP INDEX IF EXISTS idx_eval_runs_status_created;

ALTER TABLE eval_runs
    DROP COLUMN IF EXISTS lease_owner,
    DROP COLUMN IF EXISTS lease_expires_at,
    DROP COLUMN IF EXISTS lease_epoch;
