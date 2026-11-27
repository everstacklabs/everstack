DROP TABLE IF EXISTS api_key_activity_daily;
ALTER TABLE api_keys DROP COLUMN IF EXISTS rate_limit_rpm;
