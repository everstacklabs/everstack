-- Sandbox-by-default for code scorers (evaluations overhaul, Decision B).
-- Code scorers execute arbitrary user code; flipping the column default and
-- backfilling existing code scorers makes sandbox isolation the default.
-- Running a code scorer unsandboxed is a server-gated escape hatch
-- (evalrunner RunnerOpts.AllowUnsandboxedScorers, default false), not a
-- per-config choice alone.
ALTER TABLE score_configs ALTER COLUMN use_sandbox SET DEFAULT true;

-- Backfill only existing code scorers (scorer_code present); LLM-judge and
-- builtin configs are left untouched.
UPDATE score_configs
SET use_sandbox = true
WHERE use_sandbox = false
  AND scorer_code IS NOT NULL
  AND scorer_code <> '';
