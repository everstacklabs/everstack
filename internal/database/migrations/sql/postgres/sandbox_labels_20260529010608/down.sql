DROP INDEX IF EXISTS idx_sandbox_instances_labels_gin;
ALTER TABLE sandbox_instances DROP COLUMN IF EXISTS labels;
