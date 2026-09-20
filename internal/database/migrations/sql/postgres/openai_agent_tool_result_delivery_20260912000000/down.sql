ALTER TABLE openai_agent_tool_calls
    DROP COLUMN IF EXISTS delivery_claimed_until,
    DROP COLUMN IF EXISTS delivery_token,
    DROP COLUMN IF EXISTS delivery_status;
