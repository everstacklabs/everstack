-- Revert to original FK constraints without CASCADE
ALTER TABLE eval_runs
    DROP CONSTRAINT IF EXISTS eval_runs_dataset_id_fkey,
    ADD CONSTRAINT eval_runs_dataset_id_fkey
        FOREIGN KEY (dataset_id) REFERENCES datasets(id);

ALTER TABLE eval_run_items
    DROP CONSTRAINT IF EXISTS eval_run_items_dataset_item_id_fkey,
    ADD CONSTRAINT eval_run_items_dataset_item_id_fkey
        FOREIGN KEY (dataset_item_id) REFERENCES dataset_items(id);

-- Drop composite indexes
DROP INDEX IF EXISTS idx_eval_run_items_run_status;
DROP INDEX IF EXISTS idx_eval_runs_tenant_status;
