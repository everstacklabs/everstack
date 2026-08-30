-- Braintrust-style scorer fields on score_configs (additive).
--
-- Splits the overloaded data_type into two orthogonal axes and adds the rich
-- LLM-judge config the new full-page editor needs. data_type is kept as-is
-- (legacy, display-only) so every existing scorer-config id referenced by eval
-- runs, annotation queues, scheduled evals, and sampling rules keeps resolving.
--
--   scorer_type  : how the score is computed
--                  manual | llm_judge | typescript | javascript | python | builtin
--   output_type  : the score's shape
--                  numeric | boolean | categorical | choice
--
-- The backfill mirrors the RUNTIME DISPATCH ORDER (eval_runner/runner.go:436:
-- builtin_ prefix -> scorer_code+scorer_language -> eval_prompt -> manual), NOT
-- a data_type string table, because a legacy row can carry both data_type=NUMERIC
-- and a non-empty eval_prompt and still run as an LLM judge.

ALTER TABLE score_configs
    ADD COLUMN IF NOT EXISTS slug          VARCHAR(255)     NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS scorer_type   VARCHAR(50)      NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS output_type   VARCHAR(50)      NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS messages      JSONB            NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS model_params  JSONB            NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS choice_scores JSONB            NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS use_cot       BOOLEAN          NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS pass_threshold DOUBLE PRECISION;

-- Backfill scorer_type (dispatch order). data_type is stored uppercase.
UPDATE score_configs
SET scorer_type = CASE
        WHEN lower(data_type) LIKE 'builtin\_%' ESCAPE '\'                                   THEN 'builtin'
        WHEN COALESCE(scorer_code, '') <> '' AND COALESCE(scorer_language, '') <> ''         THEN lower(scorer_language)
        WHEN COALESCE(eval_prompt, '') <> '' OR upper(data_type) = 'LLM_JUDGE'               THEN 'llm_judge'
        WHEN upper(data_type) = 'CODE_SCORER'                                                THEN COALESCE(NULLIF(lower(scorer_language), ''), 'typescript')
        ELSE 'manual'
    END
WHERE scorer_type = '';

-- Backfill output_type from the now-populated scorer_type.
UPDATE score_configs
SET output_type = CASE
        WHEN scorer_type = 'manual' THEN lower(data_type)   -- numeric | boolean | categorical
        ELSE 'numeric'
    END
WHERE output_type = '';

-- Backfill a basic slug from name (non-unique; app owns collision handling).
UPDATE score_configs
SET slug = trim(both '-' from regexp_replace(lower(name), '[^a-z0-9]+', '-', 'g'))
WHERE slug = '';

CREATE INDEX IF NOT EXISTS idx_score_configs_scorer_type ON score_configs(scorer_type);
