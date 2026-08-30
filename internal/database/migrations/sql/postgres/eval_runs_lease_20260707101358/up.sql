ALTER TABLE eval_runs
    ADD COLUMN IF NOT EXISTS lease_owner TEXT,
    ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS lease_epoch BIGINT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_eval_runs_status_created ON eval_runs(status, created_at);

DELETE FROM eval_run_items a
USING eval_run_items b
WHERE a.eval_run_id = b.eval_run_id
  AND a.dataset_item_id = b.dataset_item_id
  AND a.ctid > b.ctid;

CREATE UNIQUE INDEX IF NOT EXISTS uq_eval_run_items_run_dataset_item
    ON eval_run_items(eval_run_id, dataset_item_id);
