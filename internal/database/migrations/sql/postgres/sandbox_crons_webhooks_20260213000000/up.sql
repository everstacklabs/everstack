-- Cron schedules for automated sandbox execution
CREATE TABLE IF NOT EXISTS sandbox_crons (
    id              BIGSERIAL PRIMARY KEY,
    tenant_id       VARCHAR(255) NOT NULL,
    sandbox_id      VARCHAR(255) NOT NULL REFERENCES sandbox_instances(id),
    session_id      VARCHAR(255) NOT NULL,
    name            VARCHAR(255) NOT NULL,
    schedule        VARCHAR(100) NOT NULL,
    command         TEXT NOT NULL,
    work_dir        VARCHAR(500) DEFAULT '/workspace',
    timeout_seconds INT NOT NULL DEFAULT 300,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    last_run_at     TIMESTAMPTZ,
    next_run_at     TIMESTAMPTZ,
    run_count       INT NOT NULL DEFAULT 0,
    error_count     INT NOT NULL DEFAULT 0,
    last_error      TEXT,
    auto_recreate   BOOLEAN NOT NULL DEFAULT false,
    sandbox_config  JSONB DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sandbox_crons_tenant
    ON sandbox_crons (tenant_id);

CREATE INDEX IF NOT EXISTS idx_sandbox_crons_next_run
    ON sandbox_crons (next_run_at) WHERE enabled = true;

-- Webhook triggers for sandbox execution via HTTP
CREATE TABLE IF NOT EXISTS sandbox_webhooks (
    id                BIGSERIAL PRIMARY KEY,
    tenant_id         VARCHAR(255) NOT NULL,
    sandbox_id        VARCHAR(255) NOT NULL REFERENCES sandbox_instances(id),
    session_id        VARCHAR(255) NOT NULL,
    name              VARCHAR(255) NOT NULL,
    path              VARCHAR(255) NOT NULL UNIQUE,
    secret            VARCHAR(255) NOT NULL,
    command           TEXT NOT NULL,
    work_dir          VARCHAR(500) DEFAULT '/workspace',
    timeout_seconds   INT NOT NULL DEFAULT 300,
    enabled           BOOLEAN NOT NULL DEFAULT true,
    rate_limit_rpm    INT NOT NULL DEFAULT 60,
    last_triggered_at TIMESTAMPTZ,
    trigger_count     INT NOT NULL DEFAULT 0,
    error_count       INT NOT NULL DEFAULT 0,
    last_error        TEXT,
    auto_recreate     BOOLEAN NOT NULL DEFAULT false,
    sandbox_config    JSONB DEFAULT '{}',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sandbox_webhooks_tenant
    ON sandbox_webhooks (tenant_id);

CREATE INDEX IF NOT EXISTS idx_sandbox_webhooks_path
    ON sandbox_webhooks (path) WHERE enabled = true;

-- Trigger execution history for both crons and webhooks
CREATE TABLE IF NOT EXISTS sandbox_triggers (
    id              BIGSERIAL PRIMARY KEY,
    trigger_type    VARCHAR(20) NOT NULL,
    trigger_id      BIGINT NOT NULL,
    sandbox_id      VARCHAR(255) NOT NULL,
    execution_id    VARCHAR(255),
    status          VARCHAR(50) NOT NULL DEFAULT 'running',
    error           TEXT,
    duration_ms     BIGINT,
    webhook_method  VARCHAR(10),
    webhook_headers JSONB DEFAULT '{}',
    webhook_body    TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sandbox_triggers_type_id
    ON sandbox_triggers (trigger_type, trigger_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_sandbox_triggers_sandbox
    ON sandbox_triggers (sandbox_id, created_at DESC);
