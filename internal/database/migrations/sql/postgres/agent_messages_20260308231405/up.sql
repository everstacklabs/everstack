-- Cross-agent messaging table for persistent agent communication.
CREATE TABLE IF NOT EXISTS agent_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sender_agent_id UUID NOT NULL,
    recipient_agent_id UUID NOT NULL,
    tenant_id UUID NOT NULL,
    thread_id UUID,
    content TEXT NOT NULL,
    message_type VARCHAR(50) DEFAULT 'message',  -- message | task_result | delegation
    status VARCHAR(20) DEFAULT 'pending',         -- pending | delivered | read
    payload JSONB,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    delivered_at TIMESTAMPTZ,
    read_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_agent_messages_recipient ON agent_messages(recipient_agent_id, status);
CREATE INDEX IF NOT EXISTS idx_agent_messages_thread ON agent_messages(thread_id);
CREATE INDEX IF NOT EXISTS idx_agent_messages_sender ON agent_messages(sender_agent_id, created_at);
