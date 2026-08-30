CREATE TABLE IF NOT EXISTS billing_usage_records (
    id              BIGSERIAL PRIMARY KEY,
    idempotency_key VARCHAR(255) NOT NULL UNIQUE,
    tenant_id       VARCHAR(255) NOT NULL,
    resource_type   VARCHAR(64) NOT NULL,
    resource_id     VARCHAR(255),
    source_type     VARCHAR(128) NOT NULL,
    source_ref      VARCHAR(255) NOT NULL,
    metric_type     VARCHAR(128) NOT NULL,
    quantity        DOUBLE PRECISION NOT NULL DEFAULT 0,
    unit            VARCHAR(32) NOT NULL DEFAULT '',
    cost_usd        DOUBLE PRECISION NOT NULL DEFAULT 0,
    currency        VARCHAR(8) NOT NULL DEFAULT 'USD',
    status          VARCHAR(32) NOT NULL DEFAULT 'recorded',
    metadata        JSONB NOT NULL DEFAULT '{}',
    period_start    TIMESTAMPTZ,
    period_end      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_billing_usage_records_tenant_created
    ON billing_usage_records (tenant_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_billing_usage_records_source
    ON billing_usage_records (source_type, source_ref, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_billing_usage_records_resource
    ON billing_usage_records (resource_type, resource_id, created_at DESC);
