-- Short-lived SSH bearer tokens for Daytona-style sandbox access.
-- Raw token values are shown once to the caller and never persisted.
CREATE TABLE IF NOT EXISTS sandbox_ssh_tokens (
    id              VARCHAR(64) PRIMARY KEY,
    organization_id VARCHAR(255) NOT NULL,
    tenant_id       VARCHAR(255) NOT NULL,
    instance_id     VARCHAR(255) NOT NULL,
    sandbox_id      VARCHAR(255) NOT NULL REFERENCES sandbox_instances(id) ON DELETE CASCADE,
    token_hash      CHAR(64)     NOT NULL UNIQUE,
    token_prefix    VARCHAR(24)  NOT NULL,
    created_by      VARCHAR(255) NOT NULL,
    expires_at      TIMESTAMPTZ  NOT NULL,
    revoked_at      TIMESTAMPTZ,
    last_used_at    TIMESTAMPTZ,
    last_used_ip    TEXT,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sandbox_ssh_tokens_scope_active
    ON sandbox_ssh_tokens (organization_id, tenant_id, instance_id, sandbox_id, expires_at)
    WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_sandbox_ssh_tokens_hash_active
    ON sandbox_ssh_tokens (token_hash)
    WHERE revoked_at IS NULL;
