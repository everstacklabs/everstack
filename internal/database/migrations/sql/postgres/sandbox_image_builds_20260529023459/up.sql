-- Declarative image builds. Stores spec hashes for 24h caching so
-- repeated calls to POST /v1/images/build with the same spec return
-- the cached image reference instantly without rebuilding.

CREATE TABLE IF NOT EXISTS sandbox_image_builds (
    id          VARCHAR(255) PRIMARY KEY,
    tenant_id   VARCHAR(255) NOT NULL,
    spec_hash   VARCHAR(64)  NOT NULL,      -- SHA-256 of the normalized spec JSON
    spec        JSONB        NOT NULL,       -- full build spec for debugging/audit
    image_ref   TEXT         NOT NULL DEFAULT '', -- resulting OCI image reference
    base_image  TEXT         NOT NULL DEFAULT '', -- base image used
    state       VARCHAR(50)  NOT NULL DEFAULT 'pending', -- pending | building | ready | error
    error       TEXT,
    build_ms    INT,                         -- build duration in milliseconds
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW() + INTERVAL '24 hours',
    UNIQUE (tenant_id, spec_hash)
);

CREATE INDEX IF NOT EXISTS idx_sandbox_image_builds_tenant
    ON sandbox_image_builds (tenant_id, spec_hash, expires_at);
