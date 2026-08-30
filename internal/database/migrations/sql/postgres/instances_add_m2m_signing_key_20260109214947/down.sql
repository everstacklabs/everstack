-- Remove encrypted M2M signing key column from instances table
ALTER TABLE system.instances
DROP COLUMN IF EXISTS m2m_signing_key_encrypted;
