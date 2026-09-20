ALTER TABLE openai_agent_tool_calls
    ADD COLUMN IF NOT EXISTS delivery_status text,
    ADD COLUMN IF NOT EXISTS delivery_token text,
    ADD COLUMN IF NOT EXISTS delivery_claimed_until timestamptz;

-- A completed legacy call may already have been delivered. Require provider
-- reconciliation before it can be treated as submitted; never replay it blind.
UPDATE openai_agent_tool_calls
SET delivery_status = CASE WHEN status = 'completed' THEN 'uncertain' ELSE 'pending' END
WHERE delivery_status IS NULL;

ALTER TABLE openai_agent_tool_calls
    ALTER COLUMN delivery_status SET DEFAULT 'pending',
    ALTER COLUMN delivery_status SET NOT NULL;
