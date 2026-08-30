-- Agent trigger definitions
CREATE TABLE IF NOT EXISTS agent_triggers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    name VARCHAR(255) NOT NULL,
    trigger_type VARCHAR(20) NOT NULL,   -- 'cron' | 'webhook' | 'event'
    enabled BOOLEAN NOT NULL DEFAULT TRUE,

    -- Cron config
    cron_expression VARCHAR(100),
    cron_timezone VARCHAR(50) DEFAULT 'UTC',

    -- Webhook config
    webhook_secret_hash VARCHAR(128),
    webhook_path VARCHAR(255),

    -- Event config
    event_source_agent_id UUID,
    event_type VARCHAR(50),              -- 'session.end' | 'session.error'
    event_filter JSONB,

    -- Execution config
    input_template TEXT,
    max_retries INTEGER DEFAULT 0,
    retry_delay_seconds INTEGER DEFAULT 60,
    timeout_seconds INTEGER DEFAULT 300,
    max_concurrent INTEGER DEFAULT 1,

    -- Circuit breaker
    consecutive_failures INTEGER DEFAULT 0,
    circuit_state VARCHAR(10) DEFAULT 'closed',
    circuit_opened_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_agent_triggers_agent ON agent_triggers(agent_id);
CREATE INDEX idx_agent_triggers_cron ON agent_triggers(trigger_type, enabled) WHERE trigger_type = 'cron' AND enabled = TRUE;
CREATE INDEX idx_agent_triggers_webhook ON agent_triggers(webhook_path) WHERE trigger_type = 'webhook';
CREATE INDEX idx_agent_triggers_event ON agent_triggers(event_source_agent_id, event_type) WHERE trigger_type = 'event' AND enabled = TRUE;

-- Trigger execution history
CREATE TABLE IF NOT EXISTS agent_trigger_executions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    trigger_id UUID NOT NULL REFERENCES agent_triggers(id) ON DELETE CASCADE,
    session_id UUID,
    status VARCHAR(20) NOT NULL,         -- pending | running | completed | failed | timeout | skipped
    trigger_payload JSONB,
    input_rendered TEXT,
    output_preview TEXT,
    error_message TEXT,
    attempt INTEGER DEFAULT 1,
    duration_ms INTEGER,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX idx_agent_trigger_executions_trigger ON agent_trigger_executions(trigger_id, started_at DESC);
CREATE INDEX idx_agent_trigger_executions_status ON agent_trigger_executions(status) WHERE status IN ('pending', 'running');
