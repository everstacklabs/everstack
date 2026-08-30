-- Add frozen canonical-input columns to eval_run_items so eval-run comparison
-- can match rows by input hash (not just dataset_item_id FK).
-- input_canonical: the FULL canonical input (JSONB), frozen at item INSERT.
-- input_hash: hex sha256 of the canonical bytes, computed in Go.
--   NEVER re-hash from the JSONB column: Postgres jsonb reorders/dedupes keys,
--   so jsonb round-trip bytes are not the canonical bytes that were hashed.
-- input_hash is TEXT (not CHAR(64)): bpchar padding is a join/scan foot-gun.
-- Both nullable; existing rows stay NULL until backfilled.
ALTER TABLE eval_run_items ADD COLUMN IF NOT EXISTS input_canonical JSONB;
ALTER TABLE eval_run_items ADD COLUMN IF NOT EXISTS input_hash TEXT;

CREATE INDEX IF NOT EXISTS idx_eval_run_items_run_input_hash ON eval_run_items(eval_run_id, input_hash);
