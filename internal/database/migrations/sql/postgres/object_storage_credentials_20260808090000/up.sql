-- Store object-storage provider credentials as tenant-bound ciphertext behind
-- opaque references. Existing plaintext columns remain temporarily so every
-- replica can deploy the compatible reader before backfill clears historical
-- material and enables encrypted writes.

CREATE TABLE IF NOT EXISTS object_storage_credentials (
    id          VARCHAR(96)  NOT NULL,
    tenant_id   VARCHAR(255) NOT NULL,
    backend     VARCHAR(32)  NOT NULL DEFAULT 'postgres',
    generation  BIGINT       GENERATED ALWAYS AS IDENTITY,
    ciphertext BYTEA         NOT NULL,
    key_id      VARCHAR(128) NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    rotated_at  TIMESTAMPTZ,
    revoked_at  TIMESTAMPTZ,
    PRIMARY KEY (id),
    UNIQUE (id, tenant_id)
);

CREATE INDEX IF NOT EXISTS idx_object_storage_credentials_tenant
    ON object_storage_credentials (tenant_id);

-- Existing installations start behind an explicit cutover gate so a rolling
-- deployment can place the new reader on every replica before plaintext is
-- removed. The gate starts closed even on an empty database because an empty
-- upgrade can still have old replicas serving traffic.
CREATE TABLE IF NOT EXISTS object_storage_credential_state (
    singleton       BOOLEAN     PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    cutover_enabled BOOLEAN     NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO object_storage_credential_state (singleton, cutover_enabled, updated_at)
VALUES (TRUE, FALSE, NOW())
ON CONFLICT (singleton) DO NOTHING;

ALTER TABLE object_storage_configs
    ADD COLUMN IF NOT EXISTS credential_ref VARCHAR(96);

ALTER TABLE tenant_volume_buckets
    ADD COLUMN IF NOT EXISTS credential_ref VARCHAR(96);

CREATE UNIQUE INDEX IF NOT EXISTS idx_object_storage_configs_credential_ref
    ON object_storage_configs (tenant_id, credential_ref)
    WHERE credential_ref IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_tenant_volume_buckets_credential_ref
    ON tenant_volume_buckets (tenant_id, credential_ref)
    WHERE credential_ref IS NOT NULL;

DO $do$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'object_storage_configs_credential_ref_fk'
          AND conrelid = 'object_storage_configs'::regclass
    ) THEN
        ALTER TABLE object_storage_configs
            ADD CONSTRAINT object_storage_configs_credential_ref_fk
            FOREIGN KEY (credential_ref, tenant_id)
            REFERENCES object_storage_credentials (id, tenant_id)
            ON DELETE RESTRICT;
    END IF;
END
$do$;

DO $do$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'tenant_volume_buckets_credential_ref_fk'
          AND conrelid = 'tenant_volume_buckets'::regclass
    ) THEN
        ALTER TABLE tenant_volume_buckets
            ADD CONSTRAINT tenant_volume_buckets_credential_ref_fk
            FOREIGN KEY (credential_ref, tenant_id)
            REFERENCES object_storage_credentials (id, tenant_id)
            ON DELETE RESTRICT;
    END IF;
END
$do$;

-- Install the same dormant defense-in-depth policy used by the existing
-- storage tables. Activation remains a separate rollout after every call site
-- uses tenant-aware transactions.
DROP POLICY IF EXISTS tenant_isolation ON object_storage_credentials;
CREATE POLICY tenant_isolation ON object_storage_credentials FOR ALL
    USING (everstack.tenant_matches(tenant_id::text))
    WITH CHECK (everstack.tenant_matches(tenant_id::text));

COMMENT ON TABLE object_storage_credentials IS
    'Tenant-scoped storage-provider credential registry addressed by opaque references. Values are encrypted in PostgreSQL or owned by the selected external backend.';
COMMENT ON TABLE object_storage_credential_state IS
    'Global rollout gate enabled only after every replica can resolve encrypted storage credentials.';
COMMENT ON COLUMN object_storage_configs.credential_ref IS
    'Opaque reference to encrypted provider credentials. Never return from tenant APIs.';
COMMENT ON COLUMN tenant_volume_buckets.credential_ref IS
    'Opaque reference to encrypted bucket-scoped provider credentials.';
