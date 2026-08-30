-- Short code for sandboxes — bitly-style opaque identifier used as the
-- SSH username and the preview URL subdomain on *.evs.run. Distinct from
-- the user-editable `name` column: short_code is generated, ascii-safe,
-- collision-checked, and stable for the sandbox's lifetime.
--
-- Length 8 chars from a 49-char alphabet (no 0/O/1/l/I) gives ~3.3e13
-- keyspace — collision probability is effectively zero for any realistic
-- sandbox count, but we still keep a UNIQUE constraint and let the
-- generator retry on the rare collision.
--
-- Idempotent — safe to apply on databases that already have the column.

ALTER TABLE sandbox_instances
    ADD COLUMN IF NOT EXISTS short_code VARCHAR(16);

CREATE UNIQUE INDEX IF NOT EXISTS sandbox_instances_short_code_unique_idx
    ON sandbox_instances (short_code)
    WHERE short_code IS NOT NULL;
