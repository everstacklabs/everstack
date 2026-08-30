-- Restore a single upsert target for non-tenant provider rows.
--
-- The org-scope migration (provider_configurations_org_scope_20260506151200)
-- dropped the global UNIQUE(provider_name) and replaced it with a partial
-- unique index on (organization_id, provider_name) WHERE organization_id IS
-- NOT NULL. A follow-up (provider_configurations_catalog_unique_20260506190541)
-- added a second partial index for catalog rows:
--   (provider_name) WHERE is_from_catalog = true AND organization_id IS NULL.
--
-- That left ONE write path unfixed: the YAML/startup reconciler upsert
-- (Repository.Upsert) writes instance-level providers with organization_id
-- IS NULL and is_from_catalog = false. No unique index covers that shape, so
-- its bare `INSERT ... ON CONFLICT (provider_name)` fails on every startup
-- with SQLSTATE 42P10 ("no unique or exclusion constraint matching the ON
-- CONFLICT specification"), crashing `evs serve` during provider init.
--
-- The fix re-establishes the pre-org-scope invariant the readers assume
-- (Get/Exists/List read by provider_name with no org filter): exactly ONE
-- non-tenant row per provider. We collapse the catalog-specific index into a
-- single partial unique index covering all non-tenant rows
--   (provider_name) WHERE organization_id IS NULL
-- and repoint BOTH Repository.Upsert and Repository.UpsertFromCatalog at that
-- arbiter. Tenant isolation is untouched: the arbiter excludes
-- organization_id IS NOT NULL, so per-tenant rows keep their own index and a
-- tenant row never collides with the shared instance/catalog row.

-- 1. Drop the catalog-only partial index first. The dedupe below flips the
--    surviving row's is_from_catalog flag to fold in catalog metadata, which
--    would transiently create two is_from_catalog=true rows and violate this
--    index mid-migration. Dropping it up front also means the dedupe runs with
--    no non-tenant uniqueness enforced (which is the point), and the new index
--    re-establishes it once duplicates are gone.
DROP INDEX IF EXISTS provider_configurations_catalog_provider_unique;

-- 2. Defensive dedupe. Some databases predating org-scope may hold two
--    org=NULL rows for one provider (a legacy is_from_catalog=false config row
--    plus a later catalog row the catalog-only arbiter never merged). The
--    unique index below would fail to build on those, so merge first.
--
--    Keep the richest row per provider: prefer one with a real API key, then
--    the most recently updated. Fold catalog metadata from the discarded rows
--    onto the survivor so we don't lose the catalog flag/status, then delete
--    the rest.
WITH ranked AS (
    SELECT id,
           provider_name,
           row_number() OVER (
               PARTITION BY provider_name
               ORDER BY (NULLIF(api_key_encrypted, '') IS NOT NULL) DESC,
                        updated_at DESC,
                        created_at DESC
           ) AS rn
    FROM provider_configurations
    WHERE organization_id IS NULL
),
keepers AS (
    SELECT id, provider_name FROM ranked WHERE rn = 1
),
folded AS (
    SELECT provider_name,
           bool_or(COALESCE(is_from_catalog, false)) AS any_catalog,
           max(NULLIF(catalog_status, '')) AS catalog_status,
           max(catalog_synced_at) AS catalog_synced_at
    FROM provider_configurations
    WHERE organization_id IS NULL
    GROUP BY provider_name
    HAVING count(*) > 1
)
UPDATE provider_configurations p
SET is_from_catalog   = COALESCE(p.is_from_catalog, false) OR f.any_catalog,
    catalog_status    = COALESCE(NULLIF(p.catalog_status, ''), f.catalog_status),
    catalog_synced_at = COALESCE(p.catalog_synced_at, f.catalog_synced_at)
FROM keepers k
JOIN folded f USING (provider_name)
WHERE p.id = k.id;

DELETE FROM provider_configurations p
WHERE p.organization_id IS NULL
  AND p.id NOT IN (
      SELECT id
      FROM (
          SELECT id,
                 row_number() OVER (
                     PARTITION BY provider_name
                     ORDER BY (NULLIF(api_key_encrypted, '') IS NOT NULL) DESC,
                              updated_at DESC,
                              created_at DESC
                 ) AS rn
          FROM provider_configurations
          WHERE organization_id IS NULL
      ) d
      WHERE d.rn = 1
  );

-- 3. Create the single partial unique index covering every non-tenant row.
--    Both Upsert and UpsertFromCatalog now infer this arbiter.
CREATE UNIQUE INDEX IF NOT EXISTS provider_configurations_nontenant_provider_unique
    ON provider_configurations (provider_name)
    WHERE organization_id IS NULL;
