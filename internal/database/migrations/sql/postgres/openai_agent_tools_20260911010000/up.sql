CREATE TABLE IF NOT EXISTS openai_agent_tool_grants (
    organization_id TEXT NOT NULL,
    instance_id TEXT NOT NULL,
    session_id TEXT NOT NULL REFERENCES openai_agent_sessions(id) ON DELETE CASCADE,
    upstream_name VARCHAR(64) NOT NULL,
    grant_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (organization_id, instance_id, session_id, upstream_name)
);

CREATE TABLE IF NOT EXISTS openai_agent_tool_calls (
    organization_id TEXT NOT NULL,
    instance_id TEXT NOT NULL,
    session_id TEXT NOT NULL REFERENCES openai_agent_sessions(id) ON DELETE CASCADE,
    turn_id TEXT NOT NULL,
    call_id TEXT NOT NULL,
    tool_name VARCHAR(64) NOT NULL,
    status VARCHAR(20) NOT NULL CHECK (status IN ('executing', 'completed', 'failed', 'recovery_required')),
    result_json JSONB,
    claimed_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (organization_id, instance_id, session_id, turn_id, call_id)
);

CREATE INDEX IF NOT EXISTS idx_openai_agent_tool_calls_recovery
    ON openai_agent_tool_calls (claimed_until) WHERE status = 'executing';

CREATE TABLE IF NOT EXISTS openai_agent_tool_approvals (
    id UUID PRIMARY KEY,
    organization_id TEXT NOT NULL,
    instance_id TEXT NOT NULL,
    session_id TEXT NOT NULL REFERENCES openai_agent_sessions(id) ON DELETE CASCADE,
    turn_id TEXT NOT NULL,
    call_id TEXT NOT NULL,
    tool_name VARCHAR(64) NOT NULL,
    resource_kind VARCHAR(32) NOT NULL,
    resource_id TEXT NOT NULL,
    arguments JSONB NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'approved', 'denied', 'expired', 'cancelled')),
    requested_by TEXT NOT NULL,
    resolved_by TEXT,
    requested_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL,
    UNIQUE (organization_id, instance_id, session_id, turn_id, call_id)
);

CREATE INDEX IF NOT EXISTS idx_openai_agent_tool_approvals_pending
    ON openai_agent_tool_approvals (organization_id, instance_id, status)
    WHERE status = 'pending';
