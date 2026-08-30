ALTER TABLE auth_config
    DROP COLUMN IF EXISTS connected_at,
    DROP COLUMN IF EXISTS cloud_gateway_url,
    DROP COLUMN IF EXISTS cloud_workspace_slug,
    DROP COLUMN IF EXISTS cloud_workspace_id,
    DROP COLUMN IF EXISTS cloud_organization_slug,
    DROP COLUMN IF EXISTS cloud_organization_id;
