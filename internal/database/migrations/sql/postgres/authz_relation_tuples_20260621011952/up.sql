-- ReBAC relationship tuples: the storage for the fine-grained authorization
-- engine (pkg/authz). Each row is "subject has relation on object", e.g.
--   organization:acme  owner   user:alice
--   workspace:prod     parent  organization:acme   (subject_relation = '')
--   workspace:prod     member  organization:acme#member (subject_relation = 'member')
--   dataset:42         viewer  user:erin
--
-- Created unqualified so it lands in the everstack schema via search_path,
-- matching the rest of the cloud control-plane schema.
CREATE TABLE IF NOT EXISTS relation_tuples (
    object_type      TEXT NOT NULL,
    object_id        TEXT NOT NULL,
    relation         TEXT NOT NULL,
    subject_type     TEXT NOT NULL,
    subject_id       TEXT NOT NULL,
    subject_relation TEXT NOT NULL DEFAULT '', -- '' = concrete subject (e.g. a user); else a userset relation
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (object_type, object_id, relation, subject_type, subject_id, subject_relation)
);

-- Forward lookup used by Check: "who are the subjects of object#relation?".
CREATE INDEX IF NOT EXISTS idx_relation_tuples_object
    ON relation_tuples (object_type, object_id, relation);

-- Reverse lookup for future expand / list-objects-for-user queries.
CREATE INDEX IF NOT EXISTS idx_relation_tuples_subject
    ON relation_tuples (subject_type, subject_id, subject_relation);
