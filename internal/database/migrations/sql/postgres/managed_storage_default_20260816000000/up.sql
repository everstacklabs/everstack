-- Managed Everstack Storage connections are logical tenant resources. Physical
-- R2 placement remains in internal-only fields and is resolved by the gateway.

ALTER TABLE object_storage_configs
    DROP CONSTRAINT IF EXISTS object_storage_configs_provider_check;

ALTER TABLE object_storage_configs
    ADD CONSTRAINT object_storage_configs_provider_check
    CHECK (provider IN ('s3', 'r2', 'minio', 'gcs', 'everstack'));

ALTER TABLE object_storage_configs
    ADD COLUMN IF NOT EXISTS management_mode VARCHAR(20) NOT NULL DEFAULT 'customer',
    ADD COLUMN IF NOT EXISTS managed_cell_id VARCHAR(96),
    ADD COLUMN IF NOT EXISTS managed_path_prefix VARCHAR(512) NOT NULL DEFAULT '';

ALTER TABLE object_storage_configs
    DROP CONSTRAINT IF EXISTS object_storage_configs_management_mode_check;

ALTER TABLE object_storage_configs
    ADD CONSTRAINT object_storage_configs_management_mode_check CHECK (
        (
            management_mode = 'customer'
            AND provider <> 'everstack'
            AND managed_cell_id IS NULL
            AND managed_path_prefix = ''
        )
        OR
        (
            management_mode = 'system'
            AND provider = 'everstack'
            AND endpoint = ''
            AND region = ''
            AND bucket = ''
            AND access_key_id = ''
            AND secret_access_key = ''
            AND credential_ref IS NULL
            AND path_prefix = ''
            AND is_default = true
            AND enabled = true
            AND managed_cell_id IS NOT NULL
            AND managed_cell_id <> ''
            AND managed_path_prefix ~ '^tenants/[0-9a-f]{64}$'
        )
    );

CREATE UNIQUE INDEX IF NOT EXISTS idx_object_storage_configs_system_managed
    ON object_storage_configs (tenant_id)
    WHERE management_mode = 'system';

COMMENT ON COLUMN object_storage_configs.management_mode IS
    'customer connections are tenant-configured; system connections are immutable Everstack-managed defaults';
COMMENT ON COLUMN object_storage_configs.managed_cell_id IS
    'Internal storage-cell reference for system-managed connections. Never return through tenant APIs.';
COMMENT ON COLUMN object_storage_configs.managed_path_prefix IS
    'Internal opaque tenant placement prefix. Never return through tenant APIs.';
