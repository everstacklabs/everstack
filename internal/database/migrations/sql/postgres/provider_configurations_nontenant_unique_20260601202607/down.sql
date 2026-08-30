-- Reverse the index shape only. The dedupe in up.sql is not reversible
-- (merged rows cannot be un-merged), which is acceptable for a forward fix.
DROP INDEX IF EXISTS provider_configurations_nontenant_provider_unique;

-- Restore the catalog-only partial unique index from
-- provider_configurations_catalog_unique_20260506190541.
CREATE UNIQUE INDEX IF NOT EXISTS provider_configurations_catalog_provider_unique
    ON provider_configurations (provider_name)
    WHERE is_from_catalog = true AND organization_id IS NULL;
