-- Add organization_id to provider_configurations and replace the global
-- UNIQUE(provider_name) with a tenant-scoped UNIQUE(organization_id,
-- provider_name) so two different tenants can each configure OpenAI / etc.
-- without colliding. Before this, the unique constraint allowed only one
-- row per provider name across the entire instance, which is the schema
-- design that fed the LLM-key cross-tenant leak: tenant A's row was the
-- only one, so tenant B's runtime requests routed to A's API key.
--
-- The column is added NULLABLE on purpose. Backfilling existing rows would
-- require deciding which tenant a given (already-shared) row belongs to,
-- and we cannot infer that from the data. Operators must assign owners
-- manually after the migration runs; rows with NULL organization_id are
-- invisible to tenant-scoped queries (see the repository layer).

ALTER TABLE provider_configurations
    ADD COLUMN IF NOT EXISTS organization_id UUID;

-- Drop the old unique constraint. The default name in Postgres is
-- "<table>_<column>_key" for inline UNIQUE.
ALTER TABLE provider_configurations
    DROP CONSTRAINT IF EXISTS provider_configurations_provider_name_key;

-- Tenant-scoped uniqueness. The partial index allows multiple legacy NULL
-- rows to coexist (one per provider, as before) until they are migrated;
-- once organization_id is populated, the (org, provider) pair must be
-- unique.
CREATE UNIQUE INDEX IF NOT EXISTS provider_configurations_org_provider_unique
    ON provider_configurations (organization_id, provider_name)
    WHERE organization_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_provider_configurations_organization_id
    ON provider_configurations (organization_id);

COMMENT ON COLUMN provider_configurations.organization_id IS
    'Owning tenant. NULL on legacy rows pre-migration. New rows must set it. Tenant-scoped queries filter by this column.';
