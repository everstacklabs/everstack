-- Loosen the UNIQUE(tenant_id, name) constraints on `datasets` and
-- `score_configs` so they no longer block "delete and recreate with the
-- same name" workflows.
--
-- Original migration (datasets_init_20260226002000) declared:
--   datasets:      UNIQUE(tenant_id, name)
--   score_configs: UNIQUE(tenant_id, name)
--
-- Both tables track soft-archive state — datasets via `archived_at`
-- (NULL = active, non-NULL = archived) and score_configs via
-- `is_archived` (boolean) — but the unique constraint applied across
-- all rows regardless of state. After a user archived "myset" and tried
-- to create a new dataset with the same name, the projection's INSERT
-- failed with 23505, the projection silently retried forever, and the
-- FE refetch saw no row — the same "create succeeded but row never
-- appeared" symptom from the LLM_JUDGE CHECK bug.
--
-- Replace each constraint with a partial unique index that only enforces
-- uniqueness across active (non-archived) rows. Any number of archived
-- rows with the same name can coexist.
--
-- Both tables already have non-partial supporting indexes
-- (idx_datasets_tenant_id, idx_score_configs_tenant_id) so the lookup
-- path isn't affected by dropping the table-level UNIQUE.

-- ---------------------------------------------------------------------------
-- datasets: replace UNIQUE(tenant_id, name) with partial index on archived_at IS NULL.
-- (datasets has no `status` column — that lives on `dataset_items`. The
--  archive marker on `datasets` itself is `archived_at`. Earlier draft
--  of this migration used `status = 'active'` and crashed every services
--  pod on startup with `column "status" does not exist`.)
-- ---------------------------------------------------------------------------

ALTER TABLE datasets
    DROP CONSTRAINT IF EXISTS uq_datasets_tenant_name;

CREATE UNIQUE INDEX IF NOT EXISTS uq_datasets_tenant_name_active
    ON datasets (tenant_id, name)
    WHERE archived_at IS NULL;

-- ---------------------------------------------------------------------------
-- score_configs: replace UNIQUE(tenant_id, name) with partial index on is_archived = false.
-- ---------------------------------------------------------------------------

ALTER TABLE score_configs
    DROP CONSTRAINT IF EXISTS uq_score_configs_tenant_name;

CREATE UNIQUE INDEX IF NOT EXISTS uq_score_configs_tenant_name_active
    ON score_configs (tenant_id, name)
    WHERE is_archived = false;
