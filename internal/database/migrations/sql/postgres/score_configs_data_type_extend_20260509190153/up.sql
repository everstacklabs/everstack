-- Extend the score_configs.data_type CHECK constraint to accept the
-- two scorer modes that ship with the FE and the runner but were left
-- out of the original migration (datasets_init_20260226002000):
--
--   * LLM_JUDGE   — prompt-driven scorer (eval_prompt + eval_model)
--   * CODE_SCORER — sandboxed-code scorer (scorer_code + scorer_language)
--
-- Without this, the projection's INSERT (uppercased data_type) hit
-- 23514 / "violates check constraint score_configs_data_type_check",
-- the projection failed silently, the FE refetch found no row, and
-- creating an LLM-judge or code scorer always appeared to "succeed"
-- with no row landing — blocking every pilot from creating non-
-- numeric scorers.

ALTER TABLE score_configs
    DROP CONSTRAINT IF EXISTS score_configs_data_type_check;

ALTER TABLE score_configs
    ADD CONSTRAINT score_configs_data_type_check
    CHECK (data_type IN ('NUMERIC', 'CATEGORICAL', 'BOOLEAN', 'LLM_JUDGE', 'CODE_SCORER'));
