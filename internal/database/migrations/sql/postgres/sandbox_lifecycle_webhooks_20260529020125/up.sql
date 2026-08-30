-- Customer-facing outgoing webhook endpoints for sandbox lifecycle events.
-- Distinct from sandbox_webhooks (incoming trigger webhooks) which drives
-- sandbox execution. This table stores WHERE to deliver events, not how
-- to handle incoming requests.
--
-- Events delivered: sandbox.started, sandbox.stopped, sandbox.archived,
-- sandbox.deleted, sandbox.error. Signing: HMAC-SHA256 via X-Everstack-Signature.

CREATE TABLE IF NOT EXISTS sandbox_lifecycle_webhook_endpoints (
    id          VARCHAR(255) PRIMARY KEY,
    tenant_id   VARCHAR(255) NOT NULL,
    url         TEXT         NOT NULL,
    -- JSON array of event strings, e.g. '["sandbox.started","sandbox.stopped"]'
    events      JSONB        NOT NULL DEFAULT '[]',
    -- HMAC signing secret (stored hashed in production; plain in MVP).
    secret      TEXT         NOT NULL DEFAULT '',
    enabled     BOOLEAN      NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sandbox_lc_webhooks_tenant
    ON sandbox_lifecycle_webhook_endpoints (tenant_id, enabled);

-- Delivery log: last 100 attempts per endpoint, for the UI.
CREATE TABLE IF NOT EXISTS sandbox_lifecycle_webhook_deliveries (
    id            BIGSERIAL    PRIMARY KEY,
    endpoint_id   VARCHAR(255) NOT NULL REFERENCES sandbox_lifecycle_webhook_endpoints(id) ON DELETE CASCADE,
    tenant_id     VARCHAR(255) NOT NULL,
    event         VARCHAR(100) NOT NULL,
    payload       JSONB        NOT NULL,
    status_code   INT,
    error         TEXT,
    duration_ms   INT,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sandbox_lc_webhook_deliveries_endpoint
    ON sandbox_lifecycle_webhook_deliveries (endpoint_id, created_at DESC);
