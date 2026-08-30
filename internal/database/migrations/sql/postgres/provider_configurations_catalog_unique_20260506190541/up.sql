-- Restore an upsert target for catalog-sourced provider rows.
--
-- Migration provider_configurations_org_scope_20260506151200 dropped the
-- old `UNIQUE(provider_name)` constraint and replaced it with a partial
-- unique index on `(organization_id, provider_name) WHERE
-- organization_id IS NOT NULL`. That correctly tenant-scopes user-owned
-- rows but left the catalog upsert path broken: catalog rows have
-- organization_id IS NULL by design (they're global metadata, not
-- tenant data), so the new partial index doesn't apply, and the
-- catalog handler's `INSERT ... ON CONFLICT (provider_name)` fails
-- with SQLSTATE 42P10 ("no unique or exclusion constraint matching
-- the ON CONFLICT specification") on every sync tick.
--
-- The fix is a complementary partial unique index covering catalog
-- rows. Catalog rows are identified by `is_from_catalog = true` and
-- always have a NULL organization_id; making (provider_name) unique
-- under that predicate gives the upsert a valid conflict target
-- without re-introducing global uniqueness for tenant rows.
CREATE UNIQUE INDEX IF NOT EXISTS provider_configurations_catalog_provider_unique
    ON provider_configurations (provider_name)
    WHERE is_from_catalog = true AND organization_id IS NULL;
