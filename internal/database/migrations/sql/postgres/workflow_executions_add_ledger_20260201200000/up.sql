ALTER TABLE workflow_executions ADD COLUMN IF NOT EXISTS ledger JSONB DEFAULT '[]';
