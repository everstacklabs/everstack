-- Agent knowledge digests (periodic bulletin synthesis)
CREATE TABLE IF NOT EXISTS agent_digests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id VARCHAR(255) NOT NULL,
    tenant_id UUID NOT NULL,
    content TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    memories_included INTEGER NOT NULL DEFAULT 0,
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_agent_digests_agent_version ON agent_digests (agent_id, version);
CREATE INDEX idx_agent_digests_tenant ON agent_digests (tenant_id);
CREATE INDEX idx_agent_digests_latest ON agent_digests (agent_id, version DESC);
