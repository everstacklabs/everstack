-- Add docker_host column for per-function Docker host targeting
-- Empty string = use global default Docker host

ALTER TABLE functions
ADD COLUMN IF NOT EXISTS docker_host TEXT DEFAULT '';

COMMENT ON COLUMN functions.docker_host IS 'Docker host for isolated execution. Empty = global default.';
