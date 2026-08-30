-- Remove docker_host column from functions table

ALTER TABLE functions
DROP COLUMN IF EXISTS docker_host;
