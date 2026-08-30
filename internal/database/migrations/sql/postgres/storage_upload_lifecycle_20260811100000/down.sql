DROP POLICY IF EXISTS tenant_isolation ON object_storage_upload_events;
DROP POLICY IF EXISTS tenant_isolation ON object_storage_uploads;

DROP TABLE IF EXISTS object_storage_upload_events;
DROP TABLE IF EXISTS object_storage_uploads;

ALTER TABLE object_storage_usage
    DROP COLUMN IF EXISTS reserved_object_count,
    DROP COLUMN IF EXISTS reserved_bytes;
