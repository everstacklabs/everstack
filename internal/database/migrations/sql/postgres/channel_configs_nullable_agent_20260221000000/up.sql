-- Make agent_id nullable so channels can operate in dispatcher mode (no default agent)
ALTER TABLE channel_configs ALTER COLUMN agent_id DROP NOT NULL;

-- Update foreign key to SET NULL on agent deletion
ALTER TABLE channel_configs DROP CONSTRAINT IF EXISTS channel_configs_agent_id_fkey;
ALTER TABLE channel_configs ADD CONSTRAINT channel_configs_agent_id_fkey
    FOREIGN KEY (agent_id) REFERENCES agent_definitions(id) ON DELETE SET NULL;
