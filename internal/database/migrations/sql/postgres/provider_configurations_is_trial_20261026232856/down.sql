SET LOCAL lock_timeout = '5s';

-- Delete the trial row first: once is_trial is gone it would be an ordinary
-- organization_id IS NULL row, and List / ListPlatformOwned would return its
-- key as a platform key.
DELETE FROM provider_configurations WHERE is_trial;

DROP INDEX IF EXISTS provider_configurations_trial_unique;
ALTER TABLE provider_configurations
    DROP CONSTRAINT IF EXISTS provider_configurations_trial_unowned;
ALTER TABLE provider_configurations
    DROP COLUMN IF EXISTS is_trial;
