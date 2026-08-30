CREATE TABLE IF NOT EXISTS workflow_executions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id UUID NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL,
    correlation_id VARCHAR(255) NOT NULL,
    trigger_type VARCHAR(50) NOT NULL DEFAULT 'manual',   -- manual | webhook | schedule | replay
    trigger_metadata JSONB DEFAULT '{}',                   -- source info (webhook URL, schedule cron, etc.)
    status VARCHAR(50) NOT NULL DEFAULT 'running',         -- running | completed | failed
    input_messages JSONB NOT NULL DEFAULT '[]',
    output_content TEXT,
    request_metadata JSONB DEFAULT '{}',                   -- auth headers / key-value pairs sent
    node_timings JSONB DEFAULT '{}',                       -- nodeId -> durationMs
    events JSONB DEFAULT '[]',                             -- full event log for replay
    resolved_model VARCHAR(255),
    resolved_provider VARCHAR(255),
    prompt_tokens INTEGER DEFAULT 0,
    completion_tokens INTEGER DEFAULT 0,
    total_tokens INTEGER DEFAULT 0,
    error_message TEXT,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    duration_ms INTEGER,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_workflow_executions_tenant_id ON workflow_executions(tenant_id);
CREATE INDEX idx_workflow_executions_workflow_id ON workflow_executions(workflow_id);
CREATE INDEX idx_workflow_executions_correlation_id ON workflow_executions(correlation_id);
CREATE INDEX idx_workflow_executions_status ON workflow_executions(status);
CREATE INDEX idx_workflow_executions_started_at ON workflow_executions(started_at DESC);
