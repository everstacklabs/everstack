ALTER TABLE openai_agent_sessions
    ADD COLUMN IF NOT EXISTS environment_cleanup_status text NOT NULL DEFAULT 'active';
