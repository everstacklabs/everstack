ALTER TABLE sandbox_crons
    DROP COLUMN IF EXISTS channel_config_id,
    DROP COLUMN IF EXISTS channel_ref,
    DROP COLUMN IF EXISTS thread_ref,
    DROP COLUMN IF EXISTS notify_message;
