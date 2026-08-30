-- Persistent sandbox volumes backed by S3-compatible object storage.
-- Volumes are FUSE-mounted inside sandboxes at boot. Shareable across
-- multiple sandboxes simultaneously via subpath isolation.

CREATE TABLE IF NOT EXISTS sandbox_volumes (
    id          VARCHAR(255) PRIMARY KEY,
    tenant_id   VARCHAR(255) NOT NULL,
    name        VARCHAR(255) NOT NULL,
    size_bytes  BIGINT       NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);

CREATE INDEX IF NOT EXISTS idx_sandbox_volumes_tenant
    ON sandbox_volumes (tenant_id, updated_at DESC);

-- Volume mounts: maps volumes to sandboxes at specific paths.
CREATE TABLE IF NOT EXISTS sandbox_volume_mounts (
    id          BIGSERIAL    PRIMARY KEY,
    volume_id   VARCHAR(255) NOT NULL REFERENCES sandbox_volumes(id) ON DELETE CASCADE,
    sandbox_id  VARCHAR(255) NOT NULL,
    tenant_id   VARCHAR(255) NOT NULL,
    mount_path  TEXT         NOT NULL,
    subpath     TEXT         NOT NULL DEFAULT '',
    read_only   BOOLEAN      NOT NULL DEFAULT false,
    mounted_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sandbox_volume_mounts_sandbox
    ON sandbox_volume_mounts (sandbox_id);
