-- Remove channels with no agent before restoring NOT NULL
DELETE FROM channel_configs WHERE agent_id IS NULL;

ALTER TABLE channel_configs ALTER COLUMN agent_id SET NOT NULL;
