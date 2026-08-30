-- Add tenant_id to the ReBAC relation_tuples so the authorization graph is
-- explicitly tenant-scoped (defense-in-depth + RLS-armable), not relying on
-- globally-unique object UUIDs alone. tenant_id is folded into the primary key
-- and both lookup indexes so the same logical tuple can exist independently per
-- tenant and every query filters by tenant first. Additive over the original
-- authz_relation_tuples migration (which may already be applied).
ALTER TABLE relation_tuples ADD COLUMN IF NOT EXISTS tenant_id TEXT NOT NULL DEFAULT '';

ALTER TABLE relation_tuples DROP CONSTRAINT IF EXISTS relation_tuples_pkey;
ALTER TABLE relation_tuples ADD CONSTRAINT relation_tuples_pkey
    PRIMARY KEY (tenant_id, object_type, object_id, relation, subject_type, subject_id, subject_relation);

DROP INDEX IF EXISTS idx_relation_tuples_object;
CREATE INDEX IF NOT EXISTS idx_relation_tuples_object
    ON relation_tuples (tenant_id, object_type, object_id, relation);

DROP INDEX IF EXISTS idx_relation_tuples_subject;
CREATE INDEX IF NOT EXISTS idx_relation_tuples_subject
    ON relation_tuples (tenant_id, subject_type, subject_id, subject_relation);
