-- Split prompt tokens into cache buckets so we can show the
-- non-overlapping cost breakdown opencode-style: fresh / cache_read /
-- cache_write. prompt_tokens stays inclusive (cached + fresh) — the
-- two new columns are subsets of it.
ALTER TABLE agent_session_turns
    ADD COLUMN cache_read_input_tokens INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN cache_write_input_tokens INTEGER NOT NULL DEFAULT 0;
