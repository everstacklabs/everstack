-- channel_messages: one row per inbound channel message, the source meter for
-- the MESSAGES_MONTHLY plan allowance.
--
-- cmd/serve/resource_counts.go has counted this table since the usage reporter
-- was written, but the table was never created and that file's count() helper
-- swallows query errors and returns 0. Every instance therefore reported zero
-- channel messages forever while the pricing page published 1k/15k/100k
-- monthly allowances against it.
--
-- Deliberately stores NO message content: this is a meter, not an archive.
-- Message bodies already live in the agent session; duplicating them here
-- would widen the blast radius of a leak for no metering benefit.
CREATE TABLE IF NOT EXISTS channel_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    channel_config_id UUID NOT NULL REFERENCES channel_configs(id) ON DELETE CASCADE,
    platform VARCHAR(32) NOT NULL,
    platform_user_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The metering read is "count this tenant's rows this month", so tenant_id
-- leads and created_at follows.
CREATE INDEX IF NOT EXISTS idx_channel_messages_tenant_created
    ON channel_messages(tenant_id, created_at);
CREATE INDEX IF NOT EXISTS idx_channel_messages_config
    ON channel_messages(channel_config_id);

-- Tenant isolation policy, installed dormant to match
-- rls_tenant_isolation_20260507141542: policies exist so a future per-table
-- ALTER TABLE ... ENABLE ROW LEVEL SECURITY needs no new policy work. The
-- to_regclass guard keeps this migration a no-op against the services-side
-- database, which has no channels tables.
DO $do$
BEGIN
    IF to_regclass('channel_messages') IS NULL THEN
        RETURN;
    END IF;
    IF to_regclass('everstack.tenant_matches') IS NULL
       AND NOT EXISTS (
           SELECT 1 FROM pg_proc p
           JOIN pg_namespace n ON n.oid = p.pronamespace
           WHERE n.nspname = 'everstack' AND p.proname = 'tenant_matches'
       ) THEN
        RETURN;
    END IF;
    EXECUTE 'DROP POLICY IF EXISTS tenant_isolation ON channel_messages';
    EXECUTE 'CREATE POLICY tenant_isolation ON channel_messages FOR ALL '
         || 'USING (everstack.tenant_matches(tenant_id::text)) '
         || 'WITH CHECK (everstack.tenant_matches(tenant_id::text))';
END
$do$;
