-- Troopers: first-class intelligent agent environments
CREATE TABLE IF NOT EXISTS troopers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    status VARCHAR(30) NOT NULL DEFAULT 'created',

    -- Embedded agent config
    model VARCHAR(255) NOT NULL,
    system_prompt TEXT,
    tools TEXT[] DEFAULT '{}',
    agent_config JSONB NOT NULL DEFAULT '{}',
    max_turns INTEGER NOT NULL DEFAULT 0,
    max_tool_calls_per_turn INTEGER NOT NULL DEFAULT 10,
    max_steps INTEGER,

    -- Identity files
    soul_md TEXT NOT NULL DEFAULT '',
    identity_md TEXT NOT NULL DEFAULT '',
    user_md TEXT NOT NULL DEFAULT '',
    role_md TEXT NOT NULL DEFAULT '',

    -- Sandbox config (always persistent)
    sandbox_image VARCHAR(512) NOT NULL DEFAULT 'ubuntu:22.04',
    sandbox_cpu_limit REAL NOT NULL DEFAULT 1.0,
    sandbox_memory_mb INTEGER NOT NULL DEFAULT 512,
    sandbox_disk_mb INTEGER NOT NULL DEFAULT 2048,
    sandbox_timeout_seconds INTEGER NOT NULL DEFAULT 0,
    sandbox_network_mode VARCHAR(20) NOT NULL DEFAULT 'allow',
    sandbox_allowed_hosts TEXT[] DEFAULT '{}',
    sandbox_env_vars JSONB NOT NULL DEFAULT '{}',
    sandbox_ssh_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    sandbox_git_repo_url VARCHAR(1024),
    sandbox_git_branch VARCHAR(255),

    -- Database paths (within sandbox)
    db_sqlite_path VARCHAR(512) NOT NULL DEFAULT '/trooper/data/trooper.db',
    db_lancedb_path VARCHAR(512) NOT NULL DEFAULT '/trooper/data/lancedb',
    db_redb_path VARCHAR(512) NOT NULL DEFAULT '/trooper/data/trooper.redb',

    -- Workers
    max_concurrent_workers INTEGER NOT NULL DEFAULT 3,
    worker_pool_config JSONB NOT NULL DEFAULT '{}',

    -- Display
    color VARCHAR(7),
    icon VARCHAR(50),

    -- Sandbox instance ref (set when provisioned)
    sandbox_id VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,

    CONSTRAINT uq_troopers_tenant_name UNIQUE(tenant_id, name)
);

CREATE INDEX idx_troopers_tenant_id ON troopers(tenant_id);
CREATE INDEX idx_troopers_status ON troopers(status) WHERE deleted_at IS NULL;

-- Trooper links: connections between troopers, agents, and humans
CREATE TABLE IF NOT EXISTS trooper_links (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    source_trooper_id UUID NOT NULL REFERENCES troopers(id) ON DELETE CASCADE,
    target_type VARCHAR(20) NOT NULL,
    target_id VARCHAR(255) NOT NULL,
    target_name VARCHAR(255),
    link_type VARCHAR(20) NOT NULL DEFAULT 'peer',
    protocol VARCHAR(20) NOT NULL DEFAULT 'internal',
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    config JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_trooper_link UNIQUE(source_trooper_id, target_type, target_id)
);

CREATE INDEX idx_trooper_links_source ON trooper_links(source_trooper_id);
CREATE INDEX idx_trooper_links_tenant ON trooper_links(tenant_id);

-- Trooper channel bindings: which channels route to which trooper
CREATE TABLE IF NOT EXISTS trooper_channel_bindings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    trooper_id UUID NOT NULL REFERENCES troopers(id) ON DELETE CASCADE,
    channel_config_id UUID NOT NULL REFERENCES channel_configs(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_trooper_channel_binding UNIQUE(trooper_id, channel_config_id)
);

CREATE INDEX idx_wcb_trooper ON trooper_channel_bindings(trooper_id);
CREATE INDEX idx_wcb_channel ON trooper_channel_bindings(channel_config_id);
