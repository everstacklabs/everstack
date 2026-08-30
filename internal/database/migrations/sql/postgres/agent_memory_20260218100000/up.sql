-- Persistent agent memory: facts, instructions, session summaries, documents
CREATE TABLE IF NOT EXISTS agent_memories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    scope VARCHAR(20) NOT NULL DEFAULT 'agent',
    user_id VARCHAR(255),
    memory_type VARCHAR(30) NOT NULL,
    content TEXT NOT NULL,
    fact_key VARCHAR(255),
    confidence FLOAT NOT NULL DEFAULT 1.0,
    source VARCHAR(30) NOT NULL DEFAULT 'auto_extracted',
    source_session_id UUID,
    source_turn_number INTEGER,
    metadata JSONB DEFAULT '{}',
    embedding_collection_id UUID,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    access_count INTEGER NOT NULL DEFAULT 0,
    last_accessed_at TIMESTAMPTZ,
    superseded_by UUID REFERENCES agent_memories(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_agent_memories_agent ON agent_memories(agent_id);
CREATE INDEX idx_agent_memories_tenant ON agent_memories(tenant_id);
CREATE INDEX idx_agent_memories_active ON agent_memories(agent_id, is_active) WHERE is_active = TRUE;
CREATE INDEX idx_agent_memories_type ON agent_memories(agent_id, memory_type);
CREATE INDEX idx_agent_memories_fact_key ON agent_memories(agent_id, fact_key) WHERE fact_key IS NOT NULL;
CREATE INDEX idx_agent_memories_scope_user ON agent_memories(agent_id, scope, user_id) WHERE scope = 'user';

-- Track consolidation runs
CREATE TABLE IF NOT EXISTS agent_memory_consolidation_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    memories_processed INTEGER NOT NULL DEFAULT 0,
    memories_merged INTEGER NOT NULL DEFAULT 0,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_consolidation_runs_agent ON agent_memory_consolidation_runs(agent_id);
