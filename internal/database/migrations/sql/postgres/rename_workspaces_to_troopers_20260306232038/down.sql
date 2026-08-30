-- Revert troopers → workspaces
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'everstack' AND table_name = 'troopers') THEN
        ALTER TABLE troopers RENAME TO workspaces;
        ALTER INDEX IF EXISTS idx_troopers_tenant_id RENAME TO idx_workspaces_tenant_id;
        ALTER INDEX IF EXISTS idx_troopers_status RENAME TO idx_workspaces_status;
        ALTER TABLE workspaces RENAME CONSTRAINT uq_troopers_tenant_name TO uq_workspaces_tenant_name;
    END IF;

    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'everstack' AND table_name = 'trooper_links') THEN
        ALTER TABLE trooper_links RENAME COLUMN source_trooper_id TO source_workspace_id;
        ALTER TABLE trooper_links RENAME TO workspace_links;
        ALTER INDEX IF EXISTS idx_trooper_links_source RENAME TO idx_workspace_links_source;
        ALTER INDEX IF EXISTS idx_trooper_links_tenant RENAME TO idx_workspace_links_tenant;
        ALTER TABLE workspace_links RENAME CONSTRAINT uq_trooper_link TO uq_workspace_link;
    END IF;

    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'everstack' AND table_name = 'trooper_channel_bindings') THEN
        ALTER TABLE trooper_channel_bindings RENAME COLUMN trooper_id TO workspace_id;
        ALTER TABLE trooper_channel_bindings RENAME TO workspace_channel_bindings;
        ALTER INDEX IF EXISTS idx_wcb_trooper RENAME TO idx_wcb_workspace;
        ALTER TABLE workspace_channel_bindings RENAME CONSTRAINT uq_trooper_channel_binding TO uq_workspace_channel_binding;
    END IF;

    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'everstack' AND table_name = 'agent_sessions' AND column_name = 'trooper_id'
    ) THEN
        ALTER TABLE agent_sessions RENAME COLUMN trooper_id TO workspace_id;
        ALTER INDEX IF EXISTS idx_agent_sessions_trooper_id RENAME TO idx_agent_sessions_workspace_id;
    END IF;

    ALTER INDEX IF EXISTS idx_sandbox_trooper_agent RENAME TO idx_sandbox_workspace_agent;
    ALTER INDEX IF EXISTS idx_sandbox_trooper_tenant_count RENAME TO idx_sandbox_workspace_tenant_count;
END $$;
