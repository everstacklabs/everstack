CREATE TABLE IF NOT EXISTS github_apps (
    id                        BIGSERIAL PRIMARY KEY,
    tenant_id                 VARCHAR(255) NOT NULL UNIQUE,
    app_id                    BIGINT NOT NULL UNIQUE,
    app_slug                  VARCHAR(255) NOT NULL,
    app_name                  VARCHAR(255) NOT NULL,
    private_key_encrypted     TEXT NOT NULL,
    webhook_secret_encrypted  TEXT NOT NULL,
    webhook_key               VARCHAR(255) NOT NULL UNIQUE,
    setup_url                 TEXT,
    html_url                  TEXT,
    status                    VARCHAR(50) NOT NULL DEFAULT 'active',
    created_at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_github_apps_tenant_status
    ON github_apps (tenant_id, status);

CREATE TABLE IF NOT EXISTS github_manifest_sessions (
    state        VARCHAR(255) PRIMARY KEY,
    tenant_id    VARCHAR(255) NOT NULL,
    webhook_key  VARCHAR(255) NOT NULL,
    return_to    TEXT NOT NULL DEFAULT '/settings/integrations',
    expires_at   TIMESTAMPTZ NOT NULL,
    used_at      TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_github_manifest_sessions_expires
    ON github_manifest_sessions (expires_at);

ALTER TABLE github_app_installations
    ADD COLUMN IF NOT EXISTS github_app_id BIGINT REFERENCES github_apps(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_github_installations_app
    ON github_app_installations (github_app_id);
