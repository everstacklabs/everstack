-- Remove network configuration columns from functions table

DROP INDEX IF EXISTS idx_functions_network_mode;

ALTER TABLE functions
DROP COLUMN IF EXISTS network_mode;

ALTER TABLE functions
DROP COLUMN IF EXISTS allowed_hosts;

ALTER TABLE functions
DROP COLUMN IF EXISTS vcpus;
