-- Rollback: Remove local_instance_id column from instances table
DROP INDEX IF EXISTS idx_instances_local_instance_id;
ALTER TABLE system.instances DROP COLUMN IF EXISTS local_instance_id;


