DROP INDEX IF EXISTS idx_eval_run_items_run_input_hash;

ALTER TABLE eval_run_items DROP COLUMN IF EXISTS input_hash;
ALTER TABLE eval_run_items DROP COLUMN IF EXISTS input_canonical;
