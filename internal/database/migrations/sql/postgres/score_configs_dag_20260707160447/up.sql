ALTER TABLE score_configs
    ADD COLUMN IF NOT EXISTS dag_definition JSONB;
