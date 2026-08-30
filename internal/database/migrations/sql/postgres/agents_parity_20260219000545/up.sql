ALTER TABLE agent_definitions
  ADD COLUMN IF NOT EXISTS mode VARCHAR(32) NOT NULL DEFAULT 'primary',
  ADD COLUMN IF NOT EXISTS max_steps INTEGER,
  ADD COLUMN IF NOT EXISTS task_permission_mode VARCHAR(32) NOT NULL DEFAULT 'ask',
  ADD COLUMN IF NOT EXISTS hidden BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS color VARCHAR(32),
  ADD COLUMN IF NOT EXISTS working_directory TEXT,
  ADD COLUMN IF NOT EXISTS mention_alias VARCHAR(255);

UPDATE agent_definitions
SET mode = COALESCE(NULLIF(TRIM(mode), ''), 'primary'),
    task_permission_mode = COALESCE(NULLIF(TRIM(task_permission_mode), ''), 'ask'),
    hidden = COALESCE(hidden, FALSE)
WHERE mode IS NULL OR task_permission_mode IS NULL OR hidden IS NULL
   OR TRIM(mode) = '' OR TRIM(task_permission_mode) = '';

-- Fast filters for user-facing and sub-agent selection.
CREATE INDEX IF NOT EXISTS idx_agent_definitions_mode
  ON agent_definitions(mode);

CREATE INDEX IF NOT EXISTS idx_agent_definitions_hidden
  ON agent_definitions(hidden);

CREATE INDEX IF NOT EXISTS idx_agent_definitions_mention_alias
  ON agent_definitions(tenant_id, mention_alias)
  WHERE mention_alias IS NOT NULL AND deleted_at IS NULL;
