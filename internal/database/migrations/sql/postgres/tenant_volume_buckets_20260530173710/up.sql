-- Per-tenant R2 bucket + long-lived bucket-scoped credentials for volumes.
--
-- Model (issue #317, slice 2): each tenant gets a dedicated R2 bucket; a volume
-- is a prefix (volumes/{id}/) within it. Isolation is at the S3 layer — the
-- stored token is scoped to this one bucket, so a token leaked from a sandbox
-- reaches only that tenant's own data. Long-lived (no TTL) to avoid the FUSE
-- credential-refresh problem.
--
-- NOTE: secret_access_key is sensitive; it must be encrypted at rest / sealed
-- before this is enabled in production (tracked follow-up).

CREATE TABLE IF NOT EXISTS tenant_volume_buckets (
    tenant_id         VARCHAR(255) PRIMARY KEY,
    bucket_name       VARCHAR(255) NOT NULL,
    endpoint          TEXT         NOT NULL,
    -- R2 S3 credentials scoped to bucket_name. access_key_id = token id.
    access_key_id     TEXT         NOT NULL,
    secret_access_key TEXT         NOT NULL,
    -- Cloudflare token id, kept for revocation on tenant teardown.
    cf_token_id       VARCHAR(255) NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
