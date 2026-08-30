-- Rollback: Remove signing_key column from instances table
ALTER TABLE system.instances DROP COLUMN IF EXISTS signing_key;


