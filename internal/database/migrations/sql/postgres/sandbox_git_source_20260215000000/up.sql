ALTER TABLE sandbox_instances
    ADD COLUMN IF NOT EXISTS git_repo_url         VARCHAR(500),
    ADD COLUMN IF NOT EXISTS git_branch           VARCHAR(255),
    ADD COLUMN IF NOT EXISTS git_commit_sha        VARCHAR(64),
    ADD COLUMN IF NOT EXISTS git_installation_id   BIGINT,
    ADD COLUMN IF NOT EXISTS git_cloned_at         TIMESTAMPTZ;

-- Durable quota accounting for repo clones and workspace snapshots
CREATE TABLE IF NOT EXISTS tenant_storage_usage (
    tenant_id         VARCHAR(255) NOT NULL,
    resource_type     VARCHAR(50)  NOT NULL,  -- 'repo_clone', 'workspace_snapshot'
    total_bytes       BIGINT NOT NULL DEFAULT 0,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, resource_type)
);
