CREATE TABLE IF NOT EXISTS agent_approval_reviews (
    id UUID PRIMARY KEY,
    session_id UUID NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    turn_number INTEGER NOT NULL,
    iteration INTEGER NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'denied', 'expired', 'cancelled')),
    tool_calls JSONB NOT NULL DEFAULT '[]',
    decisions JSONB,
    default_action VARCHAR(10) NOT NULL DEFAULT 'deny' CHECK (default_action IN ('approve', 'deny')),
    requested_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    resolved_at TIMESTAMPTZ,
    resolved_by VARCHAR(255),
    resolution_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_approval_reviews_pending ON agent_approval_reviews(status) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_approval_reviews_pending_expires ON agent_approval_reviews(expires_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_approval_reviews_session ON agent_approval_reviews(session_id);
CREATE INDEX IF NOT EXISTS idx_approval_reviews_tenant_status ON agent_approval_reviews(tenant_id, status);
