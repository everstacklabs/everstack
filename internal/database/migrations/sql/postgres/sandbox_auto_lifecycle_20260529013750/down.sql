DROP INDEX IF EXISTS idx_sandbox_instances_archive_due;
ALTER TABLE sandbox_instances
    DROP COLUMN IF EXISTS auto_archive_after_days,
    DROP COLUMN IF EXISTS auto_delete_after_days,
    DROP COLUMN IF EXISTS archived_at;
