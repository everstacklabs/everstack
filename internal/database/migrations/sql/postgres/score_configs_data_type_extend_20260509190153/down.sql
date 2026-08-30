-- Revert score_configs.data_type CHECK to the original three values.
-- Note: rows with data_type IN ('LLM_JUDGE', 'CODE_SCORER') that exist
-- pre-rollback will fail the new check. Operators should delete or
-- re-classify those rows before running this down migration.

ALTER TABLE score_configs
    DROP CONSTRAINT IF EXISTS score_configs_data_type_check;

ALTER TABLE score_configs
    ADD CONSTRAINT score_configs_data_type_check
    CHECK (data_type IN ('NUMERIC', 'CATEGORICAL', 'BOOLEAN'));
