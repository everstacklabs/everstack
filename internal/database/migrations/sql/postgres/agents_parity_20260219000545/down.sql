DROP INDEX IF EXISTS idx_agent_definitions_mention_alias;
DROP INDEX IF EXISTS idx_agent_definitions_hidden;
DROP INDEX IF EXISTS idx_agent_definitions_mode;

ALTER TABLE agent_definitions
  DROP COLUMN IF EXISTS mention_alias,
  DROP COLUMN IF EXISTS working_directory,
  DROP COLUMN IF EXISTS color,
  DROP COLUMN IF EXISTS hidden,
  DROP COLUMN IF EXISTS task_permission_mode,
  DROP COLUMN IF EXISTS max_steps,
  DROP COLUMN IF EXISTS mode;
