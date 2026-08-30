ALTER TABLE agent_sessions
    DROP COLUMN IF EXISTS platform_user_name,
    DROP COLUMN IF EXISTS platform_user_id,
    DROP COLUMN IF EXISTS channel_config_id,
    DROP COLUMN IF EXISTS source;
