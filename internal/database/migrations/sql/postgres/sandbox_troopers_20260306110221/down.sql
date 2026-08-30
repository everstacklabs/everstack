DROP INDEX IF EXISTS idx_sandbox_trooper_tenant_count;
DROP INDEX IF EXISTS idx_sandbox_trooper_agent;
ALTER TABLE sandbox_instances DROP COLUMN IF EXISTS persistent;
ALTER TABLE sandbox_instances DROP COLUMN IF EXISTS agent_id;
