CREATE TABLE IF NOT EXISTS alert_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT 'custom',
    severity TEXT NOT NULL DEFAULT 'warning',
    builtin_key TEXT,
    metric TEXT NOT NULL,
    operator TEXT NOT NULL DEFAULT '>',
    threshold DOUBLE PRECISION NOT NULL DEFAULT 0,
    duration_seconds INTEGER NOT NULL DEFAULT 300,
    filters JSONB NOT NULL DEFAULT '{}',
    enabled BOOLEAN NOT NULL DEFAULT true,
    muted_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_alert_rules_tenant ON alert_rules(tenant_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_alert_rules_builtin ON alert_rules(tenant_id, builtin_key) WHERE builtin_key IS NOT NULL;
