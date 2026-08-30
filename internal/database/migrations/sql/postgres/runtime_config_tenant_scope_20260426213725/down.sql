-- Reverse the tenant-scope migration. Loses any per-tenant overrides;
-- only the empty-tenant rows survive collapsing back to UNIQUE(section).
DELETE FROM runtime_config WHERE tenant_id <> '';

ALTER TABLE runtime_config
    DROP CONSTRAINT IF EXISTS runtime_config_tenant_section_key;

DROP INDEX IF EXISTS idx_runtime_config_tenant_section;

ALTER TABLE runtime_config
    ADD CONSTRAINT runtime_config_section_key UNIQUE (section);

CREATE INDEX IF NOT EXISTS idx_runtime_config_section
    ON runtime_config(section);

ALTER TABLE runtime_config DROP COLUMN IF EXISTS tenant_id;
