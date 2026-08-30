DROP INDEX IF EXISTS idx_sandbox_executions_tenant_created;

ALTER TABLE sandbox_executions DROP COLUMN IF EXISTS tenant_id;
