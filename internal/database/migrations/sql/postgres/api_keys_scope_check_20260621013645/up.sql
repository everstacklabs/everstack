-- API keys must carry at least one scope (instance_id, org_id, or user_id);
-- the auth interceptor resolves the request tenant from these columns, so a key
-- with none installs an empty tenant. The columns are FK-less TEXT today, so
-- this CHECK is the minimum integrity guard. (Phase 6.)
--
-- Added NOT VALID so the migration cannot fail on any pre-existing rows that
-- violate it; new and updated rows are enforced immediately. Once any legacy
-- rows are cleaned up, a follow-up can run:
--   ALTER TABLE api_keys VALIDATE CONSTRAINT api_keys_scope_not_empty;
ALTER TABLE api_keys
    ADD CONSTRAINT api_keys_scope_not_empty
    CHECK (
        COALESCE(NULLIF(instance_id, ''), NULLIF(org_id, ''), NULLIF(user_id, '')) IS NOT NULL
    ) NOT VALID;
