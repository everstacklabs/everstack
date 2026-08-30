DROP INDEX IF EXISTS idx_relation_tuples_subject;
CREATE INDEX IF NOT EXISTS idx_relation_tuples_subject
    ON relation_tuples (subject_type, subject_id, subject_relation);

DROP INDEX IF EXISTS idx_relation_tuples_object;
CREATE INDEX IF NOT EXISTS idx_relation_tuples_object
    ON relation_tuples (object_type, object_id, relation);

ALTER TABLE relation_tuples DROP CONSTRAINT IF EXISTS relation_tuples_pkey;
ALTER TABLE relation_tuples ADD CONSTRAINT relation_tuples_pkey
    PRIMARY KEY (object_type, object_id, relation, subject_type, subject_id, subject_relation);

ALTER TABLE relation_tuples DROP COLUMN IF EXISTS tenant_id;
