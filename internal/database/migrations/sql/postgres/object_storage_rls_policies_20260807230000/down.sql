-- object_storage_configs was already covered by the earlier general tenant
-- policy migration, so rollback preserves that pre-existing policy.
DO $do$
DECLARE
    tbl text;
BEGIN
    FOREACH tbl IN ARRAY ARRAY[
        'object_storage_objects',
        'object_storage_usage'
    ]
    LOOP
        IF to_regclass(tbl) IS NOT NULL THEN
            EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', tbl);
        END IF;
    END LOOP;
END
$do$;
