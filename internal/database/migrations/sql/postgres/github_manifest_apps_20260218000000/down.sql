DROP INDEX IF EXISTS idx_github_installations_app;

ALTER TABLE github_app_installations
    DROP COLUMN IF EXISTS github_app_id;

DROP TABLE IF EXISTS github_manifest_sessions;
DROP TABLE IF EXISTS github_apps;
