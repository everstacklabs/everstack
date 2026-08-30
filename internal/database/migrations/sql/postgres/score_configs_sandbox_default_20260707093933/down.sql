-- Revert the column default only. The up-migration's backfill of existing
-- code scorers is intentionally NOT reverted: un-backfilling would re-expose
-- code scorers to in-process execution.
ALTER TABLE score_configs ALTER COLUMN use_sandbox SET DEFAULT false;
