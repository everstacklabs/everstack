-- Drop runtime_config table and related indexes

DROP INDEX IF EXISTS idx_runtime_config_section;
DROP TABLE IF EXISTS runtime_config;
