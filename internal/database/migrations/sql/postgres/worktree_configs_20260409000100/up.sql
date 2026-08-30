-- Worktree configs pushed from ewt CLI for cloud sandbox provisioning
CREATE TABLE IF NOT EXISTS worktree_configs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          UUID NOT NULL,
    user_id         UUID NOT NULL,
    project_name    TEXT NOT NULL,
    config_yaml     TEXT NOT NULL,
    config_hash     TEXT NOT NULL,
    branch          TEXT,
    git_remote      TEXT,
    pushed_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(org_id, config_hash)
);

CREATE INDEX IF NOT EXISTS idx_wt_configs_org_project ON worktree_configs(org_id, project_name);
CREATE INDEX IF NOT EXISTS idx_wt_configs_user ON worktree_configs(user_id);
