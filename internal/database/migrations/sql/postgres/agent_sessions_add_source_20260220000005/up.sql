-- Add source tracking columns to agent_sessions for messaging platform integrations
ALTER TABLE agent_sessions
    ADD COLUMN IF NOT EXISTS source VARCHAR(50) NOT NULL DEFAULT 'admin_ui',
    ADD COLUMN IF NOT EXISTS channel_config_id UUID,
    ADD COLUMN IF NOT EXISTS platform_user_id VARCHAR(255),
    ADD COLUMN IF NOT EXISTS platform_user_name VARCHAR(255);

CREATE INDEX IF NOT EXISTS idx_agent_sessions_source ON agent_sessions(source);
CREATE INDEX IF NOT EXISTS idx_agent_sessions_channel_config_id ON agent_sessions(channel_config_id);
