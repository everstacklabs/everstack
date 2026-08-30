CREATE TABLE IF NOT EXISTS sandbox_events (
    id          BIGSERIAL PRIMARY KEY,
    sandbox_id  VARCHAR(255) NOT NULL REFERENCES sandbox_instances(id),
    session_id  VARCHAR(255) NOT NULL,
    tenant_id   VARCHAR(255) NOT NULL,
    event_type  VARCHAR(100) NOT NULL,
    message     TEXT,
    metadata    JSONB DEFAULT '{}',
    duration_ms BIGINT,
    error       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sandbox_events_sandbox_created
    ON sandbox_events (sandbox_id, created_at);

CREATE INDEX IF NOT EXISTS idx_sandbox_events_session
    ON sandbox_events (session_id);

CREATE INDEX IF NOT EXISTS idx_sandbox_events_tenant_type
    ON sandbox_events (tenant_id, event_type);
