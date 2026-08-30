-- Unify agents and troopers: extend agent_definitions with persistent-agent fields
-- Troopers become persistent agents (lifecycle_mode = 'persistent')

-- Lifecycle
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS lifecycle_mode VARCHAR(20) NOT NULL DEFAULT 'ephemeral';
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS lifecycle_status VARCHAR(30) NOT NULL DEFAULT 'active';
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS icon VARCHAR(50);

-- Identity files (persistent agents only)
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS soul_md TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS identity_md TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS user_md TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS role_md TEXT NOT NULL DEFAULT '';

-- Sandbox config
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS sandbox_image VARCHAR(512);
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS sandbox_cpu_limit REAL;
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS sandbox_memory_mb INTEGER;
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS sandbox_disk_mb INTEGER;
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS sandbox_timeout_seconds INTEGER;
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS sandbox_network_mode VARCHAR(20);
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS sandbox_allowed_hosts TEXT[] DEFAULT '{}';
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS sandbox_env_vars JSONB DEFAULT '{}';
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS sandbox_ssh_enabled BOOLEAN DEFAULT FALSE;
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS sandbox_git_repo_url VARCHAR(1024);
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS sandbox_git_branch VARCHAR(255);

-- Database paths (within sandbox)
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS db_sqlite_path VARCHAR(512);
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS db_lancedb_path VARCHAR(512);
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS db_redb_path VARCHAR(512);

-- Workers
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS max_concurrent_workers INTEGER DEFAULT 3;
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS worker_pool_config JSONB DEFAULT '{}';

-- Sandbox instance ref (set when provisioned)
ALTER TABLE agent_definitions ADD COLUMN IF NOT EXISTS sandbox_id VARCHAR(255);

-- Indexes for lifecycle queries
CREATE INDEX IF NOT EXISTS idx_agent_definitions_lifecycle_mode
  ON agent_definitions(lifecycle_mode) WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_agent_definitions_lifecycle_status
  ON agent_definitions(lifecycle_status) WHERE deleted_at IS NULL;

-- Data migration: copy troopers into agent_definitions as persistent agents
INSERT INTO agent_definitions (
    id, tenant_id, name, description, model, system_prompt, tools, config,
    max_turns, max_tool_calls_per_turn, max_steps, enabled,
    lifecycle_mode, lifecycle_status, icon,
    soul_md, identity_md, user_md, role_md,
    sandbox_image, sandbox_cpu_limit, sandbox_memory_mb, sandbox_disk_mb,
    sandbox_timeout_seconds, sandbox_network_mode, sandbox_allowed_hosts,
    sandbox_env_vars, sandbox_ssh_enabled, sandbox_git_repo_url, sandbox_git_branch,
    db_sqlite_path, db_lancedb_path, db_redb_path,
    max_concurrent_workers, worker_pool_config, color, sandbox_id,
    created_at, updated_at
)
SELECT
    id, tenant_id, name, description, model, system_prompt, tools, agent_config,
    max_turns, max_tool_calls_per_turn, max_steps, TRUE,
    'persistent', status, icon,
    soul_md, identity_md, user_md, role_md,
    sandbox_image, sandbox_cpu_limit, sandbox_memory_mb, sandbox_disk_mb,
    sandbox_timeout_seconds, sandbox_network_mode, sandbox_allowed_hosts,
    sandbox_env_vars, sandbox_ssh_enabled, sandbox_git_repo_url, sandbox_git_branch,
    db_sqlite_path, db_lancedb_path, db_redb_path,
    max_concurrent_workers, worker_pool_config, color, sandbox_id,
    created_at, updated_at
FROM troopers WHERE deleted_at IS NULL
ON CONFLICT (id) DO NOTHING;

-- Point trooper sessions to agent_id, but only for trooper IDs that
-- were successfully copied into agent_definitions (skips deleted troopers
-- whose sessions still linger, avoiding FK violation).
UPDATE agent_sessions SET agent_id = trooper_id
WHERE agent_id IS NULL
  AND trooper_id IS NOT NULL
  AND trooper_id IN (SELECT id FROM agent_definitions);

-- Agent links (replaces trooper_links)
CREATE TABLE IF NOT EXISTS agent_links (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    source_agent_id UUID NOT NULL REFERENCES agent_definitions(id) ON DELETE CASCADE,
    target_type VARCHAR(20) NOT NULL,
    target_id VARCHAR(255) NOT NULL,
    target_name VARCHAR(255),
    link_type VARCHAR(20) NOT NULL DEFAULT 'peer',
    protocol VARCHAR(20) NOT NULL DEFAULT 'internal',
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    config JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_agent_link UNIQUE(source_agent_id, target_type, target_id)
);

CREATE INDEX IF NOT EXISTS idx_agent_links_source ON agent_links(source_agent_id);
CREATE INDEX IF NOT EXISTS idx_agent_links_tenant ON agent_links(tenant_id);

-- Agent channel bindings (replaces trooper_channel_bindings)
CREATE TABLE IF NOT EXISTS agent_channel_bindings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    agent_id UUID NOT NULL REFERENCES agent_definitions(id) ON DELETE CASCADE,
    channel_config_id UUID NOT NULL REFERENCES channel_configs(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_agent_channel_binding UNIQUE(agent_id, channel_config_id)
);

CREATE INDEX IF NOT EXISTS idx_acb_agent ON agent_channel_bindings(agent_id);
CREATE INDEX IF NOT EXISTS idx_acb_channel ON agent_channel_bindings(channel_config_id);

-- Copy existing trooper links into agent_links
INSERT INTO agent_links (id, tenant_id, source_agent_id, target_type, target_id, target_name, link_type, protocol, status, config, created_at, updated_at)
SELECT id, tenant_id, source_trooper_id, target_type, target_id, target_name, link_type, protocol, status, config, created_at, updated_at
FROM trooper_links ON CONFLICT (id) DO NOTHING;

-- Copy existing trooper channel bindings into agent_channel_bindings
INSERT INTO agent_channel_bindings (id, tenant_id, agent_id, channel_config_id, enabled, created_at)
SELECT id, tenant_id, trooper_id, channel_config_id, enabled, created_at
FROM trooper_channel_bindings ON CONFLICT (id) DO NOTHING;
