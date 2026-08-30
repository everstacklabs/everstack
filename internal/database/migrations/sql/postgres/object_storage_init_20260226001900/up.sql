-- Object Storage: backend configs, object metadata, and usage tracking

-- Storage backend configuration (one per tenant, or multiple for advanced)
CREATE TABLE IF NOT EXISTS object_storage_configs (
    id               VARCHAR(255) PRIMARY KEY,
    tenant_id        VARCHAR(255) NOT NULL,
    provider         VARCHAR(50)  NOT NULL CHECK (provider IN ('s3', 'r2', 'minio', 'gcs')),
    endpoint         TEXT         NOT NULL DEFAULT '',
    region           VARCHAR(100) NOT NULL DEFAULT '',
    bucket           VARCHAR(255) NOT NULL,
    access_key_id    TEXT         NOT NULL DEFAULT '',
    secret_access_key TEXT        NOT NULL DEFAULT '',
    path_prefix      VARCHAR(512) NOT NULL DEFAULT '',
    is_default       BOOLEAN      NOT NULL DEFAULT false,
    enabled          BOOLEAN      NOT NULL DEFAULT true,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_object_storage_configs_tenant
    ON object_storage_configs (tenant_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_object_storage_configs_default
    ON object_storage_configs (tenant_id) WHERE is_default = true;

-- Object metadata registry
CREATE TABLE IF NOT EXISTS object_storage_objects (
    id               VARCHAR(255) PRIMARY KEY,
    tenant_id        VARCHAR(255) NOT NULL,
    config_id        VARCHAR(255) NOT NULL REFERENCES object_storage_configs(id) ON DELETE CASCADE,
    key              TEXT         NOT NULL,
    filename         VARCHAR(512) NOT NULL DEFAULT '',
    content_type     VARCHAR(255) NOT NULL DEFAULT 'application/octet-stream',
    size_bytes       BIGINT       NOT NULL DEFAULT 0,
    checksum_sha256  VARCHAR(64)  NOT NULL DEFAULT '',
    purpose          VARCHAR(50)  NOT NULL CHECK (purpose IN ('dataset', 'artifact', 'upload', 'eval_result')),
    reference_id     VARCHAR(255) NOT NULL DEFAULT '',
    reference_type   VARCHAR(100) NOT NULL DEFAULT '',
    metadata         JSONB        NOT NULL DEFAULT '{}',
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at       TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_object_storage_objects_tenant
    ON object_storage_objects (tenant_id);

CREATE INDEX IF NOT EXISTS idx_object_storage_objects_config
    ON object_storage_objects (config_id);

CREATE INDEX IF NOT EXISTS idx_object_storage_objects_purpose
    ON object_storage_objects (tenant_id, purpose);

CREATE INDEX IF NOT EXISTS idx_object_storage_objects_reference
    ON object_storage_objects (reference_type, reference_id);

-- Usage tracking (for quota enforcement)
CREATE TABLE IF NOT EXISTS object_storage_usage (
    tenant_id    VARCHAR(255) PRIMARY KEY,
    total_bytes  BIGINT       NOT NULL DEFAULT 0,
    object_count BIGINT       NOT NULL DEFAULT 0,
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
