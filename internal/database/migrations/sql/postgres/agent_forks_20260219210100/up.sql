-- Agent forks (context branches)
CREATE TABLE IF NOT EXISTS agent_forks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id VARCHAR(255) NOT NULL,
    tenant_id UUID NOT NULL,
    agent_id VARCHAR(255) NOT NULL,
    instruction TEXT NOT NULL,
    conclusion TEXT,
    status VARCHAR(50) NOT NULL DEFAULT 'running',
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    duration_ms BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX idx_agent_forks_session ON agent_forks (session_id);
CREATE INDEX idx_agent_forks_tenant ON agent_forks (tenant_id);
CREATE INDEX idx_agent_forks_status ON agent_forks (status) WHERE status = 'running';
