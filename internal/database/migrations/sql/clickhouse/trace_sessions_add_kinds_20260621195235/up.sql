-- Add a per-session execution-kind summary so the Sessions view can show what
-- a session actually is (agent run, workflow, sandbox work, llm calls, or a
-- combo) instead of just an opaque id. Mirrors the trace_kinds column on the
-- traces list. Populated by the session aggregator from each trace's root span.
ALTER TABLE trace_sessions
    ADD COLUMN IF NOT EXISTS kinds Array(String) DEFAULT [] AFTER environment;
