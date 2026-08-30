-- Install dormant tenant-isolation policies for every object storage table.
--
-- This migration deliberately does not arm row-level security. Existing
-- storage call sites still use explicit tenant predicates on pooled database
-- connections. Arming happens in a later migration after those calls run in
-- database.RunWithTenant transactions, as documented in
-- docs/security/storage-tenant-isolation.md.

DO $do$
DECLARE
    tbl text;
BEGIN
    FOREACH tbl IN ARRAY ARRAY[
        'object_storage_configs',
        'object_storage_objects',
        'object_storage_usage'
    ]
    LOOP
        IF to_regclass(tbl) IS NULL THEN
            CONTINUE;
        END IF;

        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', tbl);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I FOR ALL '
            || 'USING (everstack.tenant_matches(tenant_id::text)) '
            || 'WITH CHECK (everstack.tenant_matches(tenant_id::text))',
            tbl
        );
    END LOOP;
END
$do$;
