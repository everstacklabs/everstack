-- The provider setup flow used to write the same credential twice: once as the
-- user's named manual key and once as a "Config API Key" with source='config'.
-- That row is not just clutter. Only source='config' inference is metered
-- against the wallet, so a config row holding a user-supplied key bills BYOK
-- traffic as platform traffic.
--
-- Remove only config rows whose credential an exact manual row already holds;
-- a genuine platform key, which no manual row duplicates, is left alone.
DELETE FROM provider_api_keys AS config_key
USING provider_api_keys AS manual_key
WHERE config_key.provider_config_id = manual_key.provider_config_id
  AND config_key.key_encrypted = manual_key.key_encrypted
  AND config_key.source = 'config'
  AND manual_key.source = 'manual';
