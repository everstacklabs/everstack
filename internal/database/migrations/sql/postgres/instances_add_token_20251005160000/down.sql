-- Rollback: Remove activation_token column
DROP INDEX IF EXISTS idx_instances_activation_token;
ALTER TABLE system.instances DROP COLUMN IF EXISTS activation_token;

