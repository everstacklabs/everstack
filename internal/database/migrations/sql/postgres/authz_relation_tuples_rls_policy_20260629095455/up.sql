-- Install the (dormant) tenant-isolation RLS policy on relation_tuples, mirroring
-- rls_tenant_isolation_20260507141542 (which predates this table). The policy is
-- INERT until scripts/db/arm-rls.sql issues ENABLE/FORCE ROW LEVEL SECURITY, so
-- installing it here cannot break reads. The authz Postgres store sets
-- app.current_tenant on every transaction (set_config), so once armed the policy
-- admits exactly the caller's tenant's rows.
--
-- Guarded so it is a no-op on the services-bundle DB, which has neither
-- relation_tuples nor everstack.tenant_matches.
DO $do$
BEGIN
    IF to_regclass('everstack.relation_tuples') IS NULL THEN
        RETURN;
    END IF;
    IF to_regprocedure('everstack.tenant_matches(text)') IS NULL THEN
        RETURN;
    END IF;
    DROP POLICY IF EXISTS tenant_isolation ON everstack.relation_tuples;
    CREATE POLICY tenant_isolation ON everstack.relation_tuples FOR ALL
        USING (everstack.tenant_matches(tenant_id))
        WITH CHECK (everstack.tenant_matches(tenant_id));
END
$do$;
