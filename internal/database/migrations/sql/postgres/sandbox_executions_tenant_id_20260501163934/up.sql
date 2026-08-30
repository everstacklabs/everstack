-- sandbox_executions had no tenant_id column. The GetSandboxOverview RPC
-- runs an unfiltered aggregate (`SELECT COUNT(*), AVG(duration_ms) FROM
-- sandbox_executions`) every 5s per active admin tab. With no index and no
-- tenant scoping it was a full-table scan on a globally-shared table, which
-- hung the overview entirely once execution history grew non-trivial. PR #21
-- band-aided this with a 2s context timeout; this migration fixes it for
-- real:
--   1. Add tenant_id column.
--   2. Backfill from sandbox_instances (FK already exists).
--   3. Index (tenant_id, created_at DESC) — covers the overview aggregate
--      and any future tenant-scoped listing/order-by-recency.
--   4. Make the column NOT NULL once backfill completes; new rows must
--      always carry a tenant.

ALTER TABLE sandbox_executions
    ADD COLUMN IF NOT EXISTS tenant_id VARCHAR(255);

UPDATE sandbox_executions se
   SET tenant_id = si.tenant_id
  FROM sandbox_instances si
 WHERE se.sandbox_id = si.id
   AND se.tenant_id IS NULL;

-- Rows whose parent sandbox row is gone (shouldn't exist given the FK, but
-- defend against a manual cleanup that left orphans). Tag with empty
-- string so the NOT NULL constraint below still passes; no real tenant
-- can match an empty string in the overview filter.
UPDATE sandbox_executions
   SET tenant_id = ''
 WHERE tenant_id IS NULL;

ALTER TABLE sandbox_executions
    ALTER COLUMN tenant_id SET NOT NULL;

ALTER TABLE sandbox_executions
    ALTER COLUMN tenant_id SET DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_sandbox_executions_tenant_created
    ON sandbox_executions (tenant_id, created_at DESC);
