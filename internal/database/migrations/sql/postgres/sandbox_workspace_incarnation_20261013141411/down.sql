DROP INDEX IF EXISTS idx_sandbox_workspace_purges_due;
DROP TABLE IF EXISTS sandbox_workspace_purges;
ALTER TABLE sandbox_instances DROP COLUMN IF EXISTS workspace_run_id;
ALTER TABLE sandbox_instances DROP COLUMN IF EXISTS workspace_incarnation;
