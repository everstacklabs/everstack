CREATE TABLE IF NOT EXISTS sandbox_instances (
    id VARCHAR(255) PRIMARY KEY,
    session_id VARCHAR(255) NOT NULL,
    tenant_id VARCHAR(255) NOT NULL,
    backend VARCHAR(50) NOT NULL,
    container_id VARCHAR(255),
    image VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    config JSONB NOT NULL DEFAULT '{}',
    instance_id VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,
    destroyed_at TIMESTAMPTZ,
    error TEXT
);

CREATE INDEX IF NOT EXISTS idx_sandbox_instances_session
    ON sandbox_instances(session_id);

CREATE INDEX IF NOT EXISTS idx_sandbox_instances_tenant
    ON sandbox_instances(tenant_id);

CREATE INDEX IF NOT EXISTS idx_sandbox_instances_status
    ON sandbox_instances(status)
    WHERE status IN ('pending', 'running');

CREATE TABLE IF NOT EXISTS sandbox_executions (
    id VARCHAR(255) PRIMARY KEY,
    sandbox_id VARCHAR(255) NOT NULL REFERENCES sandbox_instances(id),
    session_id VARCHAR(255) NOT NULL,
    tool_name VARCHAR(100) NOT NULL,
    tool_call_id VARCHAR(255),
    language VARCHAR(50),
    command TEXT,
    exit_code INT,
    stdout TEXT,
    stderr TEXT,
    duration_ms BIGINT,
    timed_out BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sandbox_executions_sandbox
    ON sandbox_executions(sandbox_id);

CREATE INDEX IF NOT EXISTS idx_sandbox_executions_session
    ON sandbox_executions(session_id);
