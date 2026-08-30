DROP INDEX IF EXISTS sandbox_instances_short_code_unique_idx;
ALTER TABLE sandbox_instances DROP COLUMN IF EXISTS short_code;
