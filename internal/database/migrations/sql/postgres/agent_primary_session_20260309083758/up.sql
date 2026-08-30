-- Add primary_session_id to agent_definitions for persistent agents.
-- The primary session is a long-lived session created at provision time
-- that all triggers (peer messages, webhooks, cron) route through.
ALTER TABLE agent_definitions
  ADD COLUMN IF NOT EXISTS primary_session_id UUID REFERENCES agent_sessions(id);
