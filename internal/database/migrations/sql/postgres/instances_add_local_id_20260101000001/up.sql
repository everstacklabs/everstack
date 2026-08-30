-- Add local_instance_id column to instances table
-- This ID is generated at first gateway startup and persists across activation
-- It is used for upgrade flows before the gateway is activated with the License Service

ALTER TABLE system.instances ADD COLUMN IF NOT EXISTS local_instance_id TEXT;

-- Generate a unique local_instance_id for existing rows that don't have one
UPDATE system.instances 
SET local_instance_id = gen_random_uuid()::text 
WHERE local_instance_id IS NULL OR local_instance_id = '';

-- Add a unique index on local_instance_id for fast lookups
CREATE UNIQUE INDEX IF NOT EXISTS idx_instances_local_instance_id 
ON system.instances(local_instance_id) 
WHERE local_instance_id IS NOT NULL AND local_instance_id != '';

COMMENT ON COLUMN system.instances.local_instance_id IS 'Locally generated instance ID, created at first startup. Used for upgrade flows and passed to License Service during activation.';


