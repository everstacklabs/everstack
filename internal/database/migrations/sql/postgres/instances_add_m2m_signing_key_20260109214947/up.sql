-- Add encrypted M2M signing key column to instances table
-- The signing key is encrypted using ChaCha20-Poly1305 with a key derived from local_instance_id
ALTER TABLE system.instances
ADD COLUMN IF NOT EXISTS m2m_signing_key_encrypted TEXT;
