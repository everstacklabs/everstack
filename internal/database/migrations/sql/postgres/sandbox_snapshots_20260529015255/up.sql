-- Named sandbox snapshots. Snapshots are reusable environment templates
-- that tenants can create from public OCI images or running sandboxes and
-- reference when creating new sandboxes. This gives agents a way to
-- pre-bake dependencies once and spin up N identical sandboxes instantly.
--
-- States: pending → building → active | error
-- Inactive: deactivated after N weeks of no use; reactivates on next use.
-- Snapshots from public images transition pending → active directly (no build).

CREATE TABLE IF NOT EXISTS sandbox_snapshots (
    id            VARCHAR(255)  PRIMARY KEY,
    tenant_id     VARCHAR(255)  NOT NULL,
    name          VARCHAR(255)  NOT NULL,
    -- state: pending | building | active | inactive | error
    state         VARCHAR(50)   NOT NULL DEFAULT 'pending',
    -- base_image is the OCI image reference used when creating sandboxes
    -- from this snapshot (e.g. "ghcr.io/everstacklabs/sandbox:base" or
    -- a custom image). For "from sandbox" snapshots this is the sandbox's image.
    base_image    TEXT          NOT NULL,
    -- from_sandbox_id links to the sandbox this was created from (nullable).
    from_sandbox_id VARCHAR(255),
    -- error stores the build failure reason when state='error'.
    error         TEXT,
    -- size_bytes is the (optional) storage size of the snapshot image.
    size_bytes    BIGINT        DEFAULT 0,
    created_at    TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    -- last_used_at tracks when a sandbox was last created from this snapshot.
    -- Used for auto-deactivation: snapshots unused for 2 weeks → inactive.
    last_used_at  TIMESTAMPTZ,
    -- Prevent duplicate names within a tenant.
    UNIQUE (tenant_id, name)
);

CREATE INDEX IF NOT EXISTS idx_sandbox_snapshots_tenant
    ON sandbox_snapshots (tenant_id, state, updated_at DESC);

-- Add snapshot_id column to sandbox_instances so create-from-snapshot
-- is recorded and queryable.
ALTER TABLE sandbox_instances
    ADD COLUMN IF NOT EXISTS snapshot_id VARCHAR(255)
        REFERENCES sandbox_snapshots(id) ON DELETE SET NULL;
