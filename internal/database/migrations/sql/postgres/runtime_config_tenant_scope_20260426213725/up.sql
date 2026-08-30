-- Tenant-scope runtime_config so customers in a hosted multi-tenant
-- gateway can edit their own settings without overwriting each other.
--
-- Before: UNIQUE(section) → one global row per section, last writer wins
-- across tenants.
-- After:  tenant_id TEXT + UNIQUE(tenant_id, section) → per-tenant rows.
--
-- Self-hosted users have a single tenant (often empty string from
-- ExtractTenantID); behaviour is unchanged for them. Hosted users get
-- their tenant_id from the auth context per request.
--
-- Existing rows: drop them. Defaults live in
-- internal/domain/runtime_config/models.go:GetDefaultConfig and are
-- materialised on first read for each tenant. The DB seed was a
-- convenience that no longer fits the per-tenant model.

ALTER TABLE runtime_config
    ADD COLUMN IF NOT EXISTS tenant_id TEXT NOT NULL DEFAULT '';

ALTER TABLE runtime_config
    DROP CONSTRAINT IF EXISTS runtime_config_section_key;

DROP INDEX IF EXISTS idx_runtime_config_section;

ALTER TABLE runtime_config
    ADD CONSTRAINT runtime_config_tenant_section_key
    UNIQUE (tenant_id, section);

CREATE INDEX IF NOT EXISTS idx_runtime_config_tenant_section
    ON runtime_config (tenant_id, section);

COMMENT ON COLUMN runtime_config.tenant_id IS 'Tenant scope. Empty string for self-hosted single-tenant deployments.';

-- Pre-existing seed rows had enable_agents=false and enable_sse=false,
-- which were intentionally off because nothing read those values. Now
-- that the new gates DO read them, those defaults would 404 every
-- agents/* request and disable SSE streaming on first deploy. Flip
-- only those two fields to true on the global (tenant_id='') row.
-- Other customizations are preserved (jsonb_set targets a single key).
UPDATE runtime_config
SET config = jsonb_set(
    jsonb_set(config, '{enable_agents}', 'true'::jsonb),
    '{enable_sse}', 'true'::jsonb
)
WHERE section = 'features'
  AND tenant_id = ''
  AND (
    coalesce(config->>'enable_agents', 'false') = 'false'
    OR coalesce(config->>'enable_sse', 'false') = 'false'
  );
