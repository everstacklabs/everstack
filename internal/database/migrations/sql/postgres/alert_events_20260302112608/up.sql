CREATE TABLE IF NOT EXISTS alert_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id TEXT NOT NULL,
    alert_rule_id UUID NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'firing',
    metric_value DOUBLE PRECISION NOT NULL DEFAULT 0,
    threshold DOUBLE PRECISION NOT NULL DEFAULT 0,
    fired_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    acknowledged_at TIMESTAMPTZ,
    acknowledged_by TEXT,
    resolved_at TIMESTAMPTZ,
    last_notified_at TIMESTAMPTZ,
    notification_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_alert_events_tenant ON alert_events(tenant_id);
CREATE INDEX IF NOT EXISTS idx_alert_events_rule ON alert_events(alert_rule_id);
CREATE INDEX IF NOT EXISTS idx_alert_events_status ON alert_events(tenant_id, status);
