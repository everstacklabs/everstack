-- User SSH keys for sandbox SSH access
CREATE TABLE IF NOT EXISTS user_ssh_keys (
    id            BIGSERIAL PRIMARY KEY,
    user_id       VARCHAR(255) NOT NULL,
    tenant_id     VARCHAR(255) NOT NULL,
    name          VARCHAR(255) NOT NULL,
    public_key    TEXT NOT NULL,
    fingerprint   VARCHAR(255) NOT NULL,
    key_type      VARCHAR(50)  NOT NULL,
    last_used_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, fingerprint)
);
CREATE INDEX IF NOT EXISTS idx_user_ssh_keys_user ON user_ssh_keys (user_id, tenant_id);

-- Per-sandbox SSH access control
CREATE TABLE IF NOT EXISTS sandbox_ssh_access (
    id          BIGSERIAL PRIMARY KEY,
    sandbox_id  VARCHAR(255) NOT NULL,
    user_id     VARCHAR(255) NOT NULL,
    tenant_id   VARCHAR(255) NOT NULL,
    granted_by  VARCHAR(255) NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(sandbox_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_sandbox_ssh_access_sandbox ON sandbox_ssh_access (sandbox_id);
