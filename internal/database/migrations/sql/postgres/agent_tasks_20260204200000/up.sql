-- Async agent task queue
CREATE TABLE IF NOT EXISTS agent_tasks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type VARCHAR(100) NOT NULL,
    tenant_id UUID NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    payload JSONB,
    result JSONB,
    error TEXT,
    priority INTEGER NOT NULL DEFAULT 0,
    max_retries INTEGER NOT NULL DEFAULT 0,
    retry_count INTEGER NOT NULL DEFAULT 0,
    timeout_seconds INTEGER NOT NULL DEFAULT 300,
    callback_url TEXT,
    metadata JSONB DEFAULT '{}',
    worker_id VARCHAR(50),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ
);

CREATE INDEX idx_agent_tasks_dequeue ON agent_tasks (type, status, priority DESC, created_at ASC)
    WHERE status = 'pending';
CREATE INDEX idx_agent_tasks_tenant ON agent_tasks (tenant_id);
CREATE INDEX idx_agent_tasks_status ON agent_tasks (status);
CREATE INDEX idx_agent_tasks_worker ON agent_tasks (worker_id) WHERE worker_id IS NOT NULL;
