-- write your UP migration SQL here
ALTER TABLE api_keys ADD COLUMN sensitive_id VARCHAR(255);