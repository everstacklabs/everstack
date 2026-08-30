DROP TABLE IF EXISTS tenant_storage_usage;

ALTER TABLE sandbox_instances
    DROP COLUMN IF EXISTS git_repo_url,
    DROP COLUMN IF EXISTS git_branch,
    DROP COLUMN IF EXISTS git_commit_sha,
    DROP COLUMN IF EXISTS git_installation_id,
    DROP COLUMN IF EXISTS git_cloned_at;
