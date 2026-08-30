DROP INDEX IF EXISTS idx_score_configs_scorer_type;

ALTER TABLE score_configs
    DROP COLUMN IF EXISTS slug,
    DROP COLUMN IF EXISTS scorer_type,
    DROP COLUMN IF EXISTS output_type,
    DROP COLUMN IF EXISTS messages,
    DROP COLUMN IF EXISTS model_params,
    DROP COLUMN IF EXISTS choice_scores,
    DROP COLUMN IF EXISTS use_cot,
    DROP COLUMN IF EXISTS pass_threshold;
