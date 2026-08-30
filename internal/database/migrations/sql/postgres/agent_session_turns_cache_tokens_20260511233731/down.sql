ALTER TABLE agent_session_turns
    DROP COLUMN IF EXISTS cache_read_input_tokens,
    DROP COLUMN IF EXISTS cache_write_input_tokens;
