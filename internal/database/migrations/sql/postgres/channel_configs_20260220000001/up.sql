-- channel_configs: messaging platform bindings to agents
CREATE TABLE IF NOT EXISTS channel_configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    agent_id UUID NOT NULL REFERENCES agent_definitions(id),
    platform VARCHAR(20) NOT NULL,  -- discord, slack, telegram
    name VARCHAR(255) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    session_mode VARCHAR(20) NOT NULL DEFAULT 'thread',  -- shared, per_user, thread
    credentials_encrypted BYTEA,  -- AES-GCM encrypted bot token JSON
    platform_config JSONB NOT NULL DEFAULT '{}',  -- allowed channels, prefixes, etc.
    max_messages_per_minute INTEGER NOT NULL DEFAULT 30,
    max_sessions_per_user INTEGER NOT NULL DEFAULT 5,
    response_format VARCHAR(20) NOT NULL DEFAULT 'auto',  -- auto, plain, rich
    max_response_length INTEGER NOT NULL DEFAULT 2000,
    max_tokens_per_day BIGINT NOT NULL DEFAULT 0,  -- 0 = unlimited
    idle_session_ttl_seconds INTEGER NOT NULL DEFAULT 3600,
    coalesce_window_ms INTEGER NOT NULL DEFAULT 3000,
    instance_affinity VARCHAR(255) DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_channel_configs_tenant_name UNIQUE(tenant_id, name)
);

CREATE INDEX idx_channel_configs_tenant_id ON channel_configs(tenant_id);
CREATE INDEX idx_channel_configs_agent_id ON channel_configs(agent_id);
CREATE INDEX idx_channel_configs_platform ON channel_configs(platform);
CREATE INDEX idx_channel_configs_enabled ON channel_configs(enabled);
