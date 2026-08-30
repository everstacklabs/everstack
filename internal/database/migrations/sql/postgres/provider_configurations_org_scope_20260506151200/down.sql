-- Reverse provider_configurations_org_scope_20260506151200/up.sql.
-- Note: re-adding the global UNIQUE(provider_name) will fail if any
-- duplicate provider_name rows exist (i.e. multiple tenants have configured
-- the same provider since the up migration ran). Operators must
-- consolidate or delete those rows before rolling back.

DROP INDEX IF EXISTS idx_provider_configurations_organization_id;
DROP INDEX IF EXISTS provider_configurations_org_provider_unique;

ALTER TABLE provider_configurations
    DROP COLUMN IF EXISTS organization_id;

ALTER TABLE provider_configurations
    ADD CONSTRAINT provider_configurations_provider_name_key UNIQUE (provider_name);
