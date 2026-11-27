-- Model trial provider row (docs/design/free-trial-onboarding.md 3.1, 4.1).
--
-- is_trial marks the one instance-level row that holds the model trial's
-- OpenAI key. That row is owned by nobody (organization_id IS NULL) but is
-- never part of the platform key pool: ListPlatformOwned excludes it, every
-- unscoped instance-level reader and writer in the repository skips it, and
-- only the gateway's trial path reads it (ListTrialOwned), behind a
-- per-request allowance check.
--
-- The row is stored under its own provider_name (openai-trial), not openai:
-- the partial unique index provider_configurations_nontenant_provider_unique
-- allows one organization_id IS NULL row per provider_name, and catalog sync
-- keeps an instance-level openai row on every gateway. Changing that index's
-- predicate would break the ON CONFLICT inference of binaries already
-- running (SQLSTATE 42P10 at boot), so the trial row takes a distinct name
-- and the gateway serves it as openai in memory.
--
-- ADD COLUMN with a constant default is metadata-only on Postgres 11+. Old
-- binaries ignore the column.
SET LOCAL lock_timeout = '5s';

ALTER TABLE provider_configurations
    ADD COLUMN IF NOT EXISTS is_trial BOOLEAN NOT NULL DEFAULT false;

-- A trial row is never owned by a tenant.
ALTER TABLE provider_configurations
    DROP CONSTRAINT IF EXISTS provider_configurations_trial_unowned;
ALTER TABLE provider_configurations
    ADD CONSTRAINT provider_configurations_trial_unowned
    CHECK (NOT is_trial OR organization_id IS NULL);

-- At most one trial row.
CREATE UNIQUE INDEX IF NOT EXISTS provider_configurations_trial_unique
    ON provider_configurations (is_trial)
    WHERE is_trial;
