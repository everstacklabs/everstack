ALTER TABLE auth_config
    ADD COLUMN IF NOT EXISTS cloud_organization_id TEXT,
    ADD COLUMN IF NOT EXISTS cloud_organization_slug TEXT,
    ADD COLUMN IF NOT EXISTS cloud_workspace_id TEXT,
    ADD COLUMN IF NOT EXISTS cloud_workspace_slug TEXT,
    ADD COLUMN IF NOT EXISTS cloud_gateway_url TEXT,
    ADD COLUMN IF NOT EXISTS connected_at TIMESTAMPTZ;
