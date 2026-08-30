-- Tenant isolation via Row-Level Security — POLICY INSTALLATION ONLY.
--
-- This migration installs the helper function and CREATEs every
-- per-table policy, but does NOT call `ALTER TABLE … ENABLE ROW LEVEL
-- SECURITY`. Without that ALTER, the policies are dormant — Postgres
-- skips policy evaluation for tables where RLS is disabled. This is
-- intentional: enabling RLS today would break every existing handler
-- that calls `s.db.SelectContext(...)` without going through
-- internal/database.RunWithTenant, because the policies would
-- evaluate to NULL (no app.current_tenant set) and return zero rows.
--
-- The staged rollout is:
--   1. (this migration) — install function + policies dormant.
--   2. Add a connection-checkout hook that sets `app.current_tenant`
--      from request context, and convert every handler / projection
--      / worker to either set the GUC explicitly or use the
--      RunWithTenant / RunWithBypass helpers.
--   3. Per-table follow-up migrations issue `ALTER TABLE <t> ENABLE
--      ROW LEVEL SECURITY; ALTER TABLE <t> FORCE ROW LEVEL SECURITY`
--      once that table's call sites are confirmed.
--
-- Why install dormant: catching the bug-rot. If a future handler
-- forgets `WHERE tenant_id = $N`, RLS — once enabled per table —
-- automatically returns zero rows instead of leaking. The policies
-- being already in place removes the friction of writing them on
-- the same day as the activation migration.
--
-- Why the per-table to_regclass guard: services/cmd/serve.go calls
-- migrations.Ensure(...) with dialect=postgres against the
-- services-side database (license / billing / cloud), which only has
-- a subset of these tables. A flat `CREATE POLICY ON foo` against a
-- missing table aborts the whole migration with `relation "foo" does
-- not exist`, leaving the services bundle in CrashLoopBackOff. The DO
-- block creates each policy only when its target table exists in the
-- current database, so the same migration runs cleanly against
-- gateway-DB (creates everything) and services-DB (creates nothing).

-- ---------------------------------------------------------------------------
-- Helper function shared by every policy. Centralises the bypass check.
-- Predicate semantics:
--   * if app.bypass_rls = 'on' (system-internal callers) → match.
--   * else if app.current_tenant is NULL/'' → no match (fail closed).
--   * else require row.tenant_id::text = app.current_tenant.
-- ---------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION everstack.tenant_matches(row_tenant_id text)
RETURNS boolean
LANGUAGE sql
STABLE
AS $$
    SELECT current_setting('app.bypass_rls', true) = 'on'
        OR (
            current_setting('app.current_tenant', true) IS NOT NULL
            AND current_setting('app.current_tenant', true) <> ''
            AND row_tenant_id = current_setting('app.current_tenant', true)
        );
$$;

COMMENT ON FUNCTION everstack.tenant_matches(text) IS
    'RLS policy helper. Returns true when the row''s tenant matches '
    'app.current_tenant, or when app.bypass_rls is on.';

-- ---------------------------------------------------------------------------
-- Policies. RLS is NOT enabled on these tables yet — these are
-- inert until a follow-up migration calls ALTER TABLE … ENABLE ROW
-- LEVEL SECURITY per-table. Each entry is (table_name, tenant_column)
-- so that voice_clone_profiles can use org_id while everything else
-- uses tenant_id.
-- ---------------------------------------------------------------------------

DO $do$
DECLARE
    rec record;
BEGIN
    FOR rec IN
        SELECT * FROM (VALUES
            -- Agents
            ('agent_definitions',      'tenant_id'),
            ('agent_sessions',         'tenant_id'),
            ('agent_approval_reviews', 'tenant_id'),
            ('agent_branches',         'tenant_id'),
            ('agent_digests',          'tenant_id'),
            ('agent_messages',         'tenant_id'),
            ('agent_triggers',         'tenant_id'),
            ('agent_deployments',      'tenant_id'),
            ('agent_jobs',             'tenant_id'),
            ('agent_memory',           'tenant_id'),

            -- Workflows
            ('workflows',              'tenant_id'),
            ('workflow_executions',    'tenant_id'),
            ('workflow_versions',      'tenant_id'),

            -- Functions
            ('functions',              'tenant_id'),

            -- Channels
            ('channel_configs',        'tenant_id'),

            -- MCP
            ('mcp_servers',            'tenant_id'),
            ('mcp_oauth_state',        'tenant_id'),

            -- Alerts
            ('alert_rules',                'tenant_id'),
            ('alert_notification_targets', 'tenant_id'),
            ('alert_events',               'tenant_id'),

            -- Voice (org_id rather than tenant_id)
            ('voice_clone_profiles',   'org_id'),

            -- Memory / Datasets / Evals / Object storage
            ('memory_stores',          'tenant_id'),
            ('memory_request_log',     'tenant_id'),
            ('datasets',               'tenant_id'),
            ('eval_runs',              'tenant_id'),
            ('object_storage_configs', 'tenant_id'),

            -- Sandbox
            ('sandbox_events',         'tenant_id'),
            ('sandbox_git_source',     'tenant_id'),
            ('sandbox_usage_records',  'tenant_id'),
            ('billing_usage_records',  'tenant_id'),

            -- SSH / user-scoped
            ('user_ssh_keys',          'tenant_id')
        ) AS t(tablename, idcol)
    LOOP
        -- to_regclass returns NULL when the table is missing in the
        -- current search_path, so we silently skip those entries.
        IF to_regclass(rec.tablename) IS NULL THEN
            CONTINUE;
        END IF;

        -- Idempotency: drop any pre-existing tenant_isolation policy on
        -- this table before recreating, so re-runs don't fail with
        -- "policy already exists" (Postgres < 15 has no
        -- CREATE POLICY IF NOT EXISTS).
        EXECUTE format(
            'DROP POLICY IF EXISTS tenant_isolation ON %I',
            rec.tablename
        );
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I FOR ALL '
            || 'USING (everstack.tenant_matches(%I::text)) '
            || 'WITH CHECK (everstack.tenant_matches(%I::text))',
            rec.tablename, rec.idcol, rec.idcol
        );
    END LOOP;
END
$do$;
