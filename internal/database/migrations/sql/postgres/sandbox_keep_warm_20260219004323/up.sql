-- Add keep_warm column to sandbox_instances for webhook/cron keep-alive support.
ALTER TABLE sandbox_instances
    ADD COLUMN IF NOT EXISTS keep_warm BOOLEAN DEFAULT FALSE;
