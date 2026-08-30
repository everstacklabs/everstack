ALTER TABLE sandbox_instances
  ADD COLUMN IF NOT EXISTS destroy_reason VARCHAR(50) DEFAULT 'manual';

-- Backfill existing destroyed rows
UPDATE sandbox_instances
  SET destroy_reason = 'manual'
  WHERE destroyed_at IS NOT NULL AND destroy_reason IS NULL;
