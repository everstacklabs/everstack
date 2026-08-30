-- Agent context summaries (compaction history)
CREATE TABLE IF NOT EXISTS agent_context_summaries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id VARCHAR(255) NOT NULL,
    tenant_id UUID NOT NULL,
    agent_id VARCHAR(255) NOT NULL,
    tier VARCHAR(50) NOT NULL,
    content TEXT NOT NULL,
    freed_tokens INTEGER NOT NULL DEFAULT 0,
    replace_start INTEGER NOT NULL,
    replace_end INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_agent_context_summaries_session ON agent_context_summaries (session_id);
CREATE INDEX idx_agent_context_summaries_tenant ON agent_context_summaries (tenant_id);
