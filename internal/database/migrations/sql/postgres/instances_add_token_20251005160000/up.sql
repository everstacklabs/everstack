-- Add activation_token column to system.instances for local caching
ALTER TABLE system.instances 
ADD COLUMN IF NOT EXISTS activation_token TEXT;

-- Add index for quick lookups
CREATE INDEX IF NOT EXISTS idx_instances_activation_token 
ON system.instances(activation_token) 
WHERE activation_token IS NOT NULL;

COMMENT ON COLUMN system.instances.activation_token IS 'Original activation token used to activate this instance (cached locally)';

