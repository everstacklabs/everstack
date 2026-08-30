-- Corrective: the shipped backfill (score_configs_scorer_fields_20260705220632)
-- stamped two row shapes divergently from derive.go effectiveScorerType, which
-- classifies both as 'manual': (a) data_type=LLM_JUDGE with empty eval_prompt,
-- (b) data_type=CODE_SCORER with empty scorer_code. Reset scorer_type='' so the
-- derive fallback reclassifies them as 'manual'. Idempotent.
UPDATE score_configs SET scorer_type = ''
WHERE (upper(data_type) = 'LLM_JUDGE'  AND COALESCE(eval_prompt, '') = '' AND COALESCE(scorer_code, '') = '')
   OR (upper(data_type) = 'CODE_SCORER' AND COALESCE(scorer_code, '') = '');
