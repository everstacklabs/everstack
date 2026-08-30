CREATE TABLE IF NOT EXISTS agent_branches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id VARCHAR(255) NOT NULL,
    batch_id UUID,
    tenant_id UUID NOT NULL,
    agent_id VARCHAR(255) NOT NULL,
    source VARCHAR(50) NOT NULL,
    instruction TEXT NOT NULL,
    conclusion TEXT,
    status VARCHAR(50) NOT NULL DEFAULT 'running',
    messages JSONB,
    tool_calls_count INTEGER DEFAULT 0,
    prompt_tokens INTEGER DEFAULT 0,
    completion_tokens INTEGER DEFAULT 0,
    total_tokens INTEGER DEFAULT 0,
    duration_ms BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX idx_agent_branches_session ON agent_branches (session_id);
CREATE INDEX idx_agent_branches_batch ON agent_branches (batch_id) WHERE batch_id IS NOT NULL;
CREATE INDEX idx_agent_branches_tenant ON agent_branches (tenant_id);
