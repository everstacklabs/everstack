-- Rename workspaces → troopers (tables, columns, indexes, constraints)
-- Only runs if old tables exist (idempotent for fresh installs)

DO $$
BEGIN
    -- 1. Rename main table
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'everstack' AND table_name = 'workspaces')
       AND NOT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'everstack' AND table_name = 'troopers') THEN
        ALTER TABLE workspaces RENAME TO troopers;
        ALTER INDEX IF EXISTS idx_workspaces_tenant_id RENAME TO idx_troopers_tenant_id;
        ALTER INDEX IF EXISTS idx_workspaces_status RENAME TO idx_troopers_status;
        ALTER TABLE troopers RENAME CONSTRAINT uq_workspaces_tenant_name TO uq_troopers_tenant_name;
    END IF;

    -- 2. Rename links table
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'everstack' AND table_name = 'workspace_links')
       AND NOT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'everstack' AND table_name = 'trooper_links') THEN
        ALTER TABLE workspace_links RENAME COLUMN source_workspace_id TO source_trooper_id;
        ALTER TABLE workspace_links RENAME TO trooper_links;
        ALTER INDEX IF EXISTS idx_workspace_links_source RENAME TO idx_trooper_links_source;
        ALTER INDEX IF EXISTS idx_workspace_links_tenant RENAME TO idx_trooper_links_tenant;
        ALTER TABLE trooper_links RENAME CONSTRAINT uq_workspace_link TO uq_trooper_link;
    END IF;

    -- 3. Rename channel bindings table
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'everstack' AND table_name = 'workspace_channel_bindings')
       AND NOT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'everstack' AND table_name = 'trooper_channel_bindings') THEN
        ALTER TABLE workspace_channel_bindings RENAME COLUMN workspace_id TO trooper_id;
        ALTER TABLE workspace_channel_bindings RENAME TO trooper_channel_bindings;
        ALTER INDEX IF EXISTS idx_wcb_workspace RENAME TO idx_wcb_trooper;
        ALTER TABLE trooper_channel_bindings RENAME CONSTRAINT uq_workspace_channel_binding TO uq_trooper_channel_binding;
    END IF;

    -- 4. Rename workspace_id column in agent_sessions
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'everstack' AND table_name = 'agent_sessions' AND column_name = 'workspace_id'
    ) THEN
        ALTER TABLE agent_sessions RENAME COLUMN workspace_id TO trooper_id;
        ALTER INDEX IF EXISTS idx_agent_sessions_workspace_id RENAME TO idx_agent_sessions_trooper_id;
    END IF;

    -- 5. Rename sandbox workspace indexes
    ALTER INDEX IF EXISTS idx_sandbox_workspace_agent RENAME TO idx_sandbox_trooper_agent;
    ALTER INDEX IF EXISTS idx_sandbox_workspace_tenant_count RENAME TO idx_sandbox_trooper_tenant_count;
END $$;
