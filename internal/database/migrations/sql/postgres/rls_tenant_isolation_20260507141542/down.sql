-- Drops every policy + the helper. Note: this migration only CREATEs
-- policies (RLS is not enabled by it), so the inverse is just DROP.
-- Per-table ENABLE ROW LEVEL SECURITY follows in separate migrations
-- and has its own down.sql.
--
-- Mirror of up.sql's to_regclass guard: the same migration runs against
-- both gateway-DB (has all tables) and services-DB (has none of them),
-- and `DROP POLICY IF EXISTS x ON <missing_table>` still errors with
-- `relation does not exist` because the IF EXISTS clause only protects
-- the policy, not the table reference.

DO $do$
DECLARE
    tbl text;
BEGIN
    FOREACH tbl IN ARRAY ARRAY[
        'agent_definitions', 'agent_sessions', 'agent_approval_reviews',
        'agent_branches', 'agent_digests', 'agent_messages',
        'agent_triggers', 'agent_deployments', 'agent_jobs', 'agent_memory',
        'workflows', 'workflow_executions', 'workflow_versions',
        'functions',
        'channel_configs',
        'mcp_servers', 'mcp_oauth_state',
        'alert_rules', 'alert_notification_targets', 'alert_events',
        'voice_clone_profiles',
        'memory_stores', 'memory_request_log', 'datasets', 'eval_runs',
        'object_storage_configs',
        'sandbox_events', 'sandbox_git_source', 'sandbox_usage_records',
        'billing_usage_records',
        'user_ssh_keys'
    ]
    LOOP
        IF to_regclass(tbl) IS NOT NULL THEN
            EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', tbl);
        END IF;
    END LOOP;
END
$do$;

DROP FUNCTION IF EXISTS everstack.tenant_matches(text);
