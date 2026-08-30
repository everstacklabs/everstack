DO $do$
BEGIN
    IF to_regclass('everstack.relation_tuples') IS NOT NULL THEN
        EXECUTE 'ALTER TABLE everstack.relation_tuples NO FORCE ROW LEVEL SECURITY';
        EXECUTE 'ALTER TABLE everstack.relation_tuples DISABLE ROW LEVEL SECURITY';
        DROP POLICY IF EXISTS tenant_isolation ON everstack.relation_tuples;
    END IF;
END
$do$;
