-- agent_definitions: blueprint/config for an agent
CREATE TABLE IF NOT EXISTS agent_definitions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    model VARCHAR(255) NOT NULL,
    system_prompt TEXT,
    tools TEXT[] DEFAULT '{}',
    config JSONB NOT NULL DEFAULT '{}',
    max_turns INTEGER NOT NULL DEFAULT 25,
    max_tool_calls_per_turn INTEGER NOT NULL DEFAULT 10,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT uq_agent_definitions_tenant_name UNIQUE(tenant_id, name)
);

CREATE INDEX idx_agent_definitions_tenant_id ON agent_definitions(tenant_id);
CREATE INDEX idx_agent_definitions_enabled ON agent_definitions(enabled);

-- agent_sessions: execution instances
CREATE TABLE IF NOT EXISTS agent_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    agent_id UUID NOT NULL REFERENCES agent_definitions(id),
    status VARCHAR(50) NOT NULL DEFAULT 'created',
    turn_count INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX idx_agent_sessions_tenant_id ON agent_sessions(tenant_id);
CREATE INDEX idx_agent_sessions_agent_id ON agent_sessions(agent_id);
CREATE INDEX idx_agent_sessions_status ON agent_sessions(status);

-- agent_session_turns: individual conversation turns
CREATE TABLE IF NOT EXISTS agent_session_turns (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    turn_number INTEGER NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    user_input TEXT,
    assistant_output TEXT,
    tool_calls JSONB DEFAULT '[]',
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    latency_ms BIGINT NOT NULL DEFAULT 0,
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    CONSTRAINT uq_session_turn_number UNIQUE(session_id, turn_number)
);

CREATE INDEX idx_agent_session_turns_session_id ON agent_session_turns(session_id);
