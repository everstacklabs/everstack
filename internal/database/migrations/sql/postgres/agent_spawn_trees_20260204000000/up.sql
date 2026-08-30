-- Agent spawn tree tracking for sub-agent hierarchies
CREATE TABLE IF NOT EXISTS agent_spawn_trees (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tree_id UUID NOT NULL,
    parent_node_id UUID,
    agent_id UUID,
    depth INTEGER NOT NULL DEFAULT 0,
    status VARCHAR(50) NOT NULL DEFAULT 'running',
    task TEXT,
    result TEXT,
    prompt_tokens INTEGER DEFAULT 0,
    completion_tokens INTEGER DEFAULT 0,
    total_tokens INTEGER DEFAULT 0,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    execution_id UUID,
    tenant_id UUID NOT NULL
);

CREATE INDEX idx_agent_spawn_trees_tree_id ON agent_spawn_trees (tree_id);
CREATE INDEX idx_agent_spawn_trees_tenant_id ON agent_spawn_trees (tenant_id);
CREATE INDEX idx_agent_spawn_trees_parent ON agent_spawn_trees (parent_node_id) WHERE parent_node_id IS NOT NULL;
CREATE INDEX idx_agent_spawn_trees_status ON agent_spawn_trees (status) WHERE status = 'running';
