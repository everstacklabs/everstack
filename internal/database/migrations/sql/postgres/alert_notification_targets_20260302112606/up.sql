CREATE TABLE IF NOT EXISTS alert_notification_targets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id TEXT NOT NULL,
    name TEXT NOT NULL,
    target_type TEXT NOT NULL DEFAULT 'channel',
    channel_config_id UUID REFERENCES channel_configs(id) ON DELETE SET NULL,
    platform_channel_ref TEXT,
    webhook_url TEXT,
    webhook_headers JSONB NOT NULL DEFAULT '{}',
    email_addresses TEXT[] NOT NULL DEFAULT '{}',
    min_severity TEXT NOT NULL DEFAULT 'warning',
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_alert_notification_targets_tenant ON alert_notification_targets(tenant_id);
