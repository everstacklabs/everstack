-- Never discard encrypted credentials on downgrade. Restore/rotate them using
-- the running version before attempting rollback of this migration.
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM a2a_remote_agents WHERE auth_ciphertext IS NOT NULL) THEN
    RAISE EXCEPTION 'external agent credentials must be migrated before downgrade';
  END IF;
END $$;
DROP TABLE IF EXISTS a2a_external_runs;
ALTER TABLE a2a_remote_agents
    DROP CONSTRAINT IF EXISTS a2a_remote_protocol,
    DROP CONSTRAINT IF EXISTS a2a_remote_secret,
    DROP COLUMN IF EXISTS protocol,
    DROP COLUMN IF EXISTS routing_tenant,
    DROP COLUMN IF EXISTS auth_ciphertext,
    DROP COLUMN IF EXISTS auth_key_id;
