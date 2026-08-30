CREATE TABLE IF NOT EXISTS sandbox_usage_records (
    id               BIGSERIAL PRIMARY KEY,
    sandbox_id       VARCHAR(255) NOT NULL,
    session_id       VARCHAR(255) NOT NULL,
    tenant_id        VARCHAR(255) NOT NULL,
    backend          VARCHAR(50) NOT NULL,
    lifecycle_event  VARCHAR(100) NOT NULL,
    reason           VARCHAR(50) NOT NULL DEFAULT '',
    period_start     TIMESTAMPTZ NOT NULL,
    period_end       TIMESTAMPTZ NOT NULL,
    duration_seconds BIGINT NOT NULL,
    cpu_limit        DOUBLE PRECISION NOT NULL DEFAULT 0,
    memory_mb        BIGINT NOT NULL DEFAULT 0,
    disk_mb          BIGINT NOT NULL DEFAULT 0,
    pricing          JSONB NOT NULL DEFAULT '{}',
    cost_total_usd   DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sandbox_usage_records_tenant_created
    ON sandbox_usage_records (tenant_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_sandbox_usage_records_sandbox
    ON sandbox_usage_records (sandbox_id, created_at DESC);

