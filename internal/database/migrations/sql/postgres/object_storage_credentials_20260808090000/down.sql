-- Once a config has been migrated, rolling this schema back would either lose
-- the only encrypted copy or require restoring plaintext. Refuse that unsafe
-- downgrade. This down migration is valid only before credential data exists.
DO $do$
BEGIN
    IF EXISTS (SELECT 1 FROM object_storage_configs WHERE credential_ref IS NOT NULL)
       OR EXISTS (SELECT 1 FROM tenant_volume_buckets WHERE credential_ref IS NOT NULL)
       OR EXISTS (SELECT 1 FROM object_storage_credentials) THEN
        RAISE EXCEPTION
            'cannot roll back object storage credentials after encrypted references have been created';
    END IF;
END
$do$;

DROP POLICY IF EXISTS tenant_isolation ON object_storage_credentials;

ALTER TABLE tenant_volume_buckets
    DROP CONSTRAINT IF EXISTS tenant_volume_buckets_credential_ref_fk;
DROP INDEX IF EXISTS idx_tenant_volume_buckets_credential_ref;
ALTER TABLE tenant_volume_buckets
    DROP COLUMN IF EXISTS credential_ref;

ALTER TABLE object_storage_configs
    DROP CONSTRAINT IF EXISTS object_storage_configs_credential_ref_fk;
DROP INDEX IF EXISTS idx_object_storage_configs_credential_ref;
ALTER TABLE object_storage_configs
    DROP COLUMN IF EXISTS credential_ref;

DROP TABLE IF EXISTS object_storage_credential_state;
DROP TABLE IF EXISTS object_storage_credentials;
