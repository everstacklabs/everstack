DROP INDEX IF EXISTS idx_agent_sessions_revision_id;

ALTER TABLE agent_sessions
    DROP CONSTRAINT IF EXISTS fk_agent_sessions_revision;
ALTER TABLE agent_sessions
    DROP COLUMN IF EXISTS revision_id;

ALTER TABLE agent_definitions
    DROP CONSTRAINT IF EXISTS fk_agent_definitions_active_revision;
ALTER TABLE agent_definitions
    DROP COLUMN IF EXISTS active_revision_id;

DROP TABLE IF EXISTS agent_revision_files;
DROP TABLE IF EXISTS agent_revisions;
