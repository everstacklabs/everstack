CREATE TABLE IF NOT EXISTS github_app_installations (
    id                    BIGSERIAL PRIMARY KEY,
    tenant_id             VARCHAR(255) NOT NULL,
    installation_id       BIGINT NOT NULL UNIQUE,
    account_login         VARCHAR(255) NOT NULL,
    account_type          VARCHAR(50)  NOT NULL,     -- "Organization" | "User"
    app_id                BIGINT NOT NULL,
    permissions           JSONB NOT NULL DEFAULT '{}',
    repository_selection  VARCHAR(50) NOT NULL DEFAULT 'all',
    status                VARCHAR(50) NOT NULL DEFAULT 'active',
    installed_by          VARCHAR(255),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_github_installations_tenant
    ON github_app_installations (tenant_id) WHERE status = 'active';

-- Webhook delivery deduplication (replay protection)
CREATE TABLE IF NOT EXISTS github_webhook_deliveries (
    delivery_id  VARCHAR(255) PRIMARY KEY,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_github_webhook_deliveries_ttl
    ON github_webhook_deliveries (received_at);
