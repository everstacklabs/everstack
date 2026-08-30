-- write your DOWN migration SQL here
ALTER TABLE api_keys DROP COLUMN revoked;
ALTER TABLE api_keys DROP COLUMN revoked_at;