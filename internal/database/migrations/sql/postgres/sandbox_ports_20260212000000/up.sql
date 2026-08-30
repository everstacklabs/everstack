CREATE TABLE IF NOT EXISTS sandbox_ports (
    id              BIGSERIAL PRIMARY KEY,
    sandbox_id      VARCHAR(255) NOT NULL REFERENCES sandbox_instances(id) ON DELETE CASCADE,
    session_id      VARCHAR(255) NOT NULL,
    tenant_id       VARCHAR(255) NOT NULL,
    port            INT NOT NULL,
    protocol        VARCHAR(10) NOT NULL DEFAULT 'tcp',
    subdomain       VARCHAR(255) NOT NULL UNIQUE,
    host_port       INT,
    backend_target  VARCHAR(500),
    status          VARCHAR(50) NOT NULL DEFAULT 'active',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at       TIMESTAMPTZ,
    UNIQUE(sandbox_id, port)
);

CREATE INDEX IF NOT EXISTS idx_sandbox_ports_subdomain_active
    ON sandbox_ports (subdomain) WHERE status = 'active';

CREATE INDEX IF NOT EXISTS idx_sandbox_ports_sandbox_active
    ON sandbox_ports (sandbox_id) WHERE status = 'active';
