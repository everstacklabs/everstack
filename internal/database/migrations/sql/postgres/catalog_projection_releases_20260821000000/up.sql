CREATE TABLE IF NOT EXISTS catalog_projection_releases (
    version               TEXT        PRIMARY KEY,
    bundle_sha256         TEXT        NOT NULL CHECK (length(bundle_sha256) = 64),
    events                JSONB       NOT NULL DEFAULT '[]'::jsonb,
    events_persisted_at   TIMESTAMPTZ,
    events_published_at   TIMESTAMPTZ,
    publication_claim_id  TEXT,
    publication_claim_at  TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at          TIMESTAMPTZ
);
