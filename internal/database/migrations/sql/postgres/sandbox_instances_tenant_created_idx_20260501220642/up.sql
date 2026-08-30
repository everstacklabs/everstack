-- ListSandboxInstances does:
--   SELECT … FROM sandbox_instances
--    WHERE tenant_id = $1 [AND status = $2]
--    ORDER BY created_at DESC
--    LIMIT $n
-- The admin sandboxes page polls this every 1.5–5s per tab. The existing
-- single-column idx_sandbox_instances_tenant covers the WHERE filter but
-- not the ORDER BY, so even a tenant-scoped slice paid for an in-memory
-- sort. This composite index covers both the filter and the recency sort
-- in one access path.
--
-- Sibling to idx_sandbox_executions_tenant_created from migration
-- sandbox_executions_tenant_id_20260501163934.
CREATE INDEX IF NOT EXISTS idx_sandbox_instances_tenant_created
    ON sandbox_instances (tenant_id, created_at DESC);
