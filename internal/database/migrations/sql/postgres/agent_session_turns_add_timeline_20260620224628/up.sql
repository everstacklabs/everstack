-- Persist the ordered per-turn timeline (interleaved assistant text, tool
-- calls, and HITL exchanges) so the UI can replay a turn in true chronological
-- order after a reload, instead of reconstructing from the flat fields.
-- Nullable: turns recorded before this column existed fall back to the flat
-- user_input / assistant_output / tool_calls fields.
ALTER TABLE agent_session_turns
    ADD COLUMN timeline JSONB;
