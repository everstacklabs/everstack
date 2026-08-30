-- Persistent troopers: add agent_id and persistent flag to sandbox_instances
ALTER TABLE sandbox_instances ADD COLUMN IF NOT EXISTS agent_id VARCHAR(255);
ALTER TABLE sandbox_instances ADD COLUMN IF NOT EXISTS persistent BOOLEAN NOT NULL DEFAULT FALSE;

-- Index for looking up the active trooper for an agent
CREATE INDEX IF NOT EXISTS idx_sandbox_trooper_agent
    ON sandbox_instances(agent_id)
    WHERE persistent = true AND lifecycle_state IN ('running', 'stopped');

-- Index for counting persistent troopers per tenant (plan limit enforcement)
CREATE INDEX IF NOT EXISTS idx_sandbox_trooper_tenant_count
    ON sandbox_instances(tenant_id)
    WHERE persistent = true AND lifecycle_state NOT IN ('terminated');
