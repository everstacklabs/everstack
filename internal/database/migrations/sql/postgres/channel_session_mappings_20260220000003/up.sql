-- channel_session_mappings: maps platform references to agent sessions
CREATE TABLE IF NOT EXISTS channel_session_mappings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_config_id UUID NOT NULL REFERENCES channel_configs(id) ON DELETE CASCADE,
    platform_channel_ref VARCHAR(255) NOT NULL,   -- Discord channel ID, Slack channel ID, etc.
    platform_user_id VARCHAR(255) NOT NULL DEFAULT '',
    platform_user_name VARCHAR(255) NOT NULL DEFAULT '',
    platform_thread_ref VARCHAR(255) NOT NULL DEFAULT '',  -- Thread ID if applicable
    agent_session_id UUID NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_message_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_channel_session_mappings_config_id ON channel_session_mappings(channel_config_id);
CREATE INDEX idx_channel_session_mappings_session_id ON channel_session_mappings(agent_session_id);
CREATE INDEX idx_channel_session_mappings_lookup ON channel_session_mappings(channel_config_id, platform_channel_ref, platform_user_id, platform_thread_ref);
