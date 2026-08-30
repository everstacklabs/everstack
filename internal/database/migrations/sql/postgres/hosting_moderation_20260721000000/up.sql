-- Enrich the original report queue and add the durable audit/outbox used by
-- the instance moderation plane. This is intentionally separate from the
-- hosting init migration so installations that already recorded that version
-- receive the new schema on upgrade.
ALTER TABLE site_abuse_reports
    ADD COLUMN IF NOT EXISTS site_id UUID REFERENCES sites(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS details TEXT,
    ADD COLUMN IF NOT EXISTS page_path TEXT,
    ADD COLUMN IF NOT EXISTS reviewed_by TEXT,
    ADD COLUMN IF NOT EXISTS resolution_note TEXT,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

ALTER TABLE sites
    ADD COLUMN IF NOT EXISTS moderation_generation BIGINT NOT NULL DEFAULT 0;

UPDATE site_abuse_reports r
SET site_id = s.id
FROM sites s
WHERE r.site_id IS NULL AND r.slug = s.slug;

-- Older code did not constrain these free-form columns. Preserve existing
-- rows by normalizing unknown values before adding the stricter queue rules.
UPDATE site_abuse_reports
SET reason = 'other'
WHERE reason NOT IN ('phishing', 'malware', 'impersonation', 'privacy', 'copyright', 'other');

UPDATE site_abuse_reports
SET status = 'open'
WHERE status NOT IN ('open', 'resolved', 'dismissed');

ALTER TABLE site_abuse_reports
    ADD CONSTRAINT ck_site_abuse_report_reason CHECK (
        reason IN ('phishing', 'malware', 'impersonation', 'privacy', 'copyright', 'other')
    ),
    ADD CONSTRAINT ck_site_abuse_report_status CHECK (status IN ('open', 'resolved', 'dismissed'));

-- site_moderation_actions doubles as the immutable audit ledger and retry
-- outbox for projecting desired takedown/restore state to the serving edge.
CREATE TABLE site_moderation_actions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    site_id UUID NOT NULL REFERENCES sites(id) ON DELETE RESTRICT,
    slug TEXT NOT NULL,
    generation BIGINT NOT NULL,
    action VARCHAR(16) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    reason TEXT,
    note TEXT,
    requested_by TEXT NOT NULL,
    idempotency_key TEXT NOT NULL UNIQUE,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    lease_token TEXT,
    lease_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    applied_at TIMESTAMPTZ,
    CONSTRAINT uq_site_moderation_generation UNIQUE(site_id, generation),
    CONSTRAINT ck_site_moderation_generation CHECK (generation > 0),
    CONSTRAINT ck_site_moderation_action CHECK (action IN ('takedown', 'restore')),
    CONSTRAINT ck_site_moderation_status CHECK (status IN ('pending', 'applied', 'superseded')),
    CONSTRAINT ck_site_moderation_reason CHECK (
        (action = 'restore' AND reason IS NULL)
        OR (
            action = 'takedown'
            AND reason IN ('phishing', 'malware', 'impersonation', 'privacy', 'copyright', 'other')
        )
    )
);

CREATE INDEX idx_site_moderation_actions_pending
    ON site_moderation_actions(status, created_at)
    WHERE status = 'pending';
CREATE INDEX idx_site_moderation_actions_slug ON site_moderation_actions(slug, created_at DESC);
