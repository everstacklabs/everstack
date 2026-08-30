-- A downgrade cannot represent managed connections in the old provider enum,
-- and deleting one would cascade to customer object metadata. Refuse the
-- downgrade until managed connections have been migrated deliberately.
DO $do$
BEGIN
    IF EXISTS (
        SELECT 1 FROM object_storage_configs WHERE management_mode = 'system'
    ) THEN
        RAISE EXCEPTION
            'cannot roll back managed storage while Everstack Storage connections exist';
    END IF;
END
$do$;

DROP INDEX IF EXISTS idx_object_storage_configs_system_managed;

ALTER TABLE object_storage_configs
    DROP CONSTRAINT IF EXISTS object_storage_configs_management_mode_check,
    DROP COLUMN IF EXISTS managed_path_prefix,
    DROP COLUMN IF EXISTS managed_cell_id,
    DROP COLUMN IF EXISTS management_mode;

ALTER TABLE object_storage_configs
    DROP CONSTRAINT IF EXISTS object_storage_configs_provider_check;

ALTER TABLE object_storage_configs
    ADD CONSTRAINT object_storage_configs_provider_check
    CHECK (provider IN ('s3', 'r2', 'minio', 'gcs'));
