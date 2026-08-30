-- Reverse unification: drop new columns and tables
-- Trooper tables remain untouched as fallback

DROP TABLE IF EXISTS agent_channel_bindings;
DROP TABLE IF EXISTS agent_links;

-- Restore trooper_id on sessions where we merged
UPDATE agent_sessions SET agent_id = NULL
WHERE agent_id IN (SELECT id FROM agent_definitions WHERE lifecycle_mode = 'persistent');

DROP INDEX IF EXISTS idx_agent_definitions_lifecycle_status;
DROP INDEX IF EXISTS idx_agent_definitions_lifecycle_mode;

ALTER TABLE agent_definitions DROP COLUMN IF EXISTS sandbox_id;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS worker_pool_config;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS max_concurrent_workers;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS db_redb_path;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS db_lancedb_path;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS db_sqlite_path;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS sandbox_git_branch;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS sandbox_git_repo_url;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS sandbox_ssh_enabled;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS sandbox_env_vars;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS sandbox_allowed_hosts;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS sandbox_network_mode;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS sandbox_timeout_seconds;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS sandbox_disk_mb;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS sandbox_memory_mb;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS sandbox_cpu_limit;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS sandbox_image;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS role_md;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS user_md;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS identity_md;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS soul_md;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS icon;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS lifecycle_status;
ALTER TABLE agent_definitions DROP COLUMN IF EXISTS lifecycle_mode;

-- Delete migrated persistent agents (their original data is still in troopers table)
DELETE FROM agent_definitions WHERE id IN (SELECT id FROM troopers WHERE deleted_at IS NULL);
