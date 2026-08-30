-- Phase 3b: hypothesis-tagged attempts on agent_branches + eval_run_items.
--
-- hypothesis_text     — free-form "what we're testing" string
--                       ("does sonnet do better with the stricter system prompt?")
-- hypothesis_diff     — structured "what changed vs the parent attempt":
--                       {model, prompt_template_id, prompt_version,
--                        sampling_params, tool_list, system_prompt_diff}
--
-- Together with parent_attempt_id (Phase 3a) these turn the verdict-rate
-- dashboards into "this prompt change beat baseline by 12pp on Claude but
-- regressed by 7pp on GPT" — the snapshot of the moment of decision that
-- the community asked for in the source feedback for this amendment.

ALTER TABLE agent_branches
    ADD COLUMN IF NOT EXISTS hypothesis_text TEXT DEFAULT '';

ALTER TABLE agent_branches
    ADD COLUMN IF NOT EXISTS hypothesis_diff JSONB DEFAULT '{}'::jsonb;

ALTER TABLE eval_run_items
    ADD COLUMN IF NOT EXISTS hypothesis_text TEXT DEFAULT '';

ALTER TABLE eval_run_items
    ADD COLUMN IF NOT EXISTS hypothesis_diff JSONB DEFAULT '{}'::jsonb;

-- No index on hypothesis_text — it's free-form and queried by joining to
-- agent_branches.id from the verdict-rate breakdown queries, not searched
-- directly. hypothesis_diff stays a small JSONB blob (typically <512 bytes)
-- so GIN indexing is overkill at current volumes; revisit if Phase 4 needs
-- key-by-key search.
