-- No CHECK: validating one would scan api_keys under an exclusive lock. The
-- bots API is the only writer and enforces 1 to 100000.
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS rate_limit_rpm INTEGER NULL;

-- No foreign key: deployed api_keys.id columns may be UUID or TEXT.
CREATE TABLE IF NOT EXISTS api_key_activity_daily (
    tenant_id TEXT NOT NULL,
    api_key_id TEXT NOT NULL,
    day DATE NOT NULL,
    surface TEXT NOT NULL,
    calls BIGINT NOT NULL,
    PRIMARY KEY (api_key_id, day, surface)
);
CREATE INDEX IF NOT EXISTS idx_api_key_activity_daily_tenant_day ON api_key_activity_daily (tenant_id, day);
