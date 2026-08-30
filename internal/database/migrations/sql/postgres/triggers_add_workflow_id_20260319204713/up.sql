ALTER TABLE agent_triggers ADD COLUMN workflow_id UUID REFERENCES workflows(id) ON DELETE SET NULL;
