ALTER TABLE sandbox_instances DROP COLUMN IF EXISTS snapshot_id;
DROP INDEX IF EXISTS idx_sandbox_snapshots_tenant;
DROP TABLE IF EXISTS sandbox_snapshots;
