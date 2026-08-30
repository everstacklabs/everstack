-- Agent-as-API Deployments: versioned, published agent configs with own auth + limits
CREATE TABLE IF NOT EXISTS agent_deployments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    name VARCHAR(255) NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    status VARCHAR(20) NOT NULL DEFAULT 'active',

    -- Frozen config at deploy time
    agent_config_snapshot JSONB NOT NULL,

    -- Limits
    rate_limit_rpm INTEGER,
    rate_limit_burst INTEGER,
    spend_limit_daily_cents INTEGER,
    max_concurrent_sessions INTEGER DEFAULT 10,
    max_turns_per_session INTEGER,
    session_timeout_seconds INTEGER DEFAULT 300,

    -- Session tracking: when false, API invocations do NOT create entries in agent_sessions
    track_sessions BOOLEAN NOT NULL DEFAULT TRUE,

    -- CORS
    allowed_origins TEXT[] DEFAULT '{}',

    -- Metadata
    description TEXT,
    changelog TEXT,
    deployed_by VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_agent_deployments_agent ON agent_deployments(agent_id);
CREATE INDEX idx_agent_deployments_active ON agent_deployments(agent_id, status) WHERE status = 'active';
CREATE UNIQUE INDEX idx_agent_deployments_version ON agent_deployments(agent_id, version);

-- Per-deployment API keys (separate from admin API keys)
CREATE TABLE IF NOT EXISTS agent_deployment_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    deployment_id UUID NOT NULL REFERENCES agent_deployments(id) ON DELETE CASCADE,
    key_hash VARCHAR(128) NOT NULL,
    key_prefix VARCHAR(12) NOT NULL,
    name VARCHAR(255),
    expires_at TIMESTAMPTZ,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_deployment_keys_hash ON agent_deployment_keys(key_hash);
CREATE INDEX idx_deployment_keys_deployment ON agent_deployment_keys(deployment_id);

-- Invocation log (lightweight, for usage tracking + debugging)
CREATE TABLE IF NOT EXISTS agent_deployment_invocations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    deployment_id UUID NOT NULL,
    session_id UUID,
    key_id UUID,
    status VARCHAR(20) NOT NULL,
    input_preview TEXT,
    output_preview TEXT,
    turns INTEGER DEFAULT 0,
    prompt_tokens INTEGER DEFAULT 0,
    completion_tokens INTEGER DEFAULT 0,
    duration_ms INTEGER,
    error_message TEXT,
    client_ip VARCHAR(45),
    user_agent VARCHAR(500),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX idx_invocations_deployment ON agent_deployment_invocations(deployment_id, created_at DESC);
CREATE INDEX idx_invocations_tenant ON agent_deployment_invocations(tenant_id, created_at DESC);
