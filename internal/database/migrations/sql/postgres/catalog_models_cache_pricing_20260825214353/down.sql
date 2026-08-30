ALTER TABLE catalog_models
    DROP COLUMN IF EXISTS cache_read_cost_per_1k,
    DROP COLUMN IF EXISTS cache_write_cost_per_1k;
