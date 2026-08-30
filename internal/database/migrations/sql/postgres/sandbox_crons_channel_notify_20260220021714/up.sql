-- Add channel notification support to sandbox_crons.
-- When a cron fires and these fields are populated, the scheduler sends
-- the output/notification message back to the originating channel.
ALTER TABLE sandbox_crons
    ADD COLUMN IF NOT EXISTS channel_config_id VARCHAR(255),
    ADD COLUMN IF NOT EXISTS channel_ref       VARCHAR(255),
    ADD COLUMN IF NOT EXISTS thread_ref        VARCHAR(255),
    ADD COLUMN IF NOT EXISTS notify_message    TEXT;
