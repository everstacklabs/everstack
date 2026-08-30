-- Fix eval_runs.dataset_id: add ON DELETE CASCADE
ALTER TABLE eval_runs
    DROP CONSTRAINT IF EXISTS eval_runs_dataset_id_fkey,
    ADD CONSTRAINT eval_runs_dataset_id_fkey
        FOREIGN KEY (dataset_id) REFERENCES datasets(id) ON DELETE CASCADE;

-- Fix eval_run_items.dataset_item_id: add ON DELETE CASCADE
ALTER TABLE eval_run_items
    DROP CONSTRAINT IF EXISTS eval_run_items_dataset_item_id_fkey,
    ADD CONSTRAINT eval_run_items_dataset_item_id_fkey
        FOREIGN KEY (dataset_item_id) REFERENCES dataset_items(id) ON DELETE CASCADE;

-- Add composite indexes for common query patterns
CREATE INDEX IF NOT EXISTS idx_eval_run_items_run_status ON eval_run_items(eval_run_id, status);
CREATE INDEX IF NOT EXISTS idx_eval_runs_tenant_status ON eval_runs(tenant_id, status);
