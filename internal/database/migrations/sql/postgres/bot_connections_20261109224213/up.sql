ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS surfaces TEXT[] NULL;
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS last_used_at TIMESTAMPTZ NULL;

-- No foreign key: deployed api_keys.id columns may be UUID or TEXT.
CREATE TABLE IF NOT EXISTS bot_connections (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id TEXT NOT NULL,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    api_key_id TEXT NOT NULL UNIQUE,
    remote_agent TEXT NULL,
    created_by TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NULL,
    UNIQUE (tenant_id, name)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_bot_connections_tenant_name ON bot_connections (tenant_id, lower(name));
CREATE INDEX IF NOT EXISTS idx_bot_connections_tenant ON bot_connections (tenant_id);
