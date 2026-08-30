ALTER TABLE eval_runs
    DROP COLUMN IF EXISTS dataset_version_id;

DROP TABLE IF EXISTS dataset_version_items;
DROP TABLE IF EXISTS dataset_versions;
