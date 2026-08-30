-- Deactivate duplicate session summaries, keeping only the most recent per (agent_id, source_session_id).
UPDATE agent_memories
SET is_active = FALSE, updated_at = NOW()
WHERE id IN (
    SELECT id FROM (
        SELECT id,
               ROW_NUMBER() OVER (
                   PARTITION BY agent_id, source_session_id
                   ORDER BY updated_at DESC, created_at DESC
               ) AS rn
        FROM agent_memories
        WHERE memory_type = 'session_summary' AND is_active = TRUE
    ) ranked
    WHERE rn > 1
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_memories_unique_session_summary
ON agent_memories (agent_id, source_session_id)
WHERE memory_type = 'session_summary' AND is_active = TRUE;
