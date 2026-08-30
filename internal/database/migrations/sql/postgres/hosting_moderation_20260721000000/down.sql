DROP TABLE IF EXISTS site_moderation_actions;

ALTER TABLE site_abuse_reports
    DROP CONSTRAINT IF EXISTS ck_site_abuse_report_reason,
    DROP CONSTRAINT IF EXISTS ck_site_abuse_report_status,
    DROP COLUMN IF EXISTS site_id,
    DROP COLUMN IF EXISTS details,
    DROP COLUMN IF EXISTS page_path,
    DROP COLUMN IF EXISTS reviewed_by,
    DROP COLUMN IF EXISTS resolution_note,
    DROP COLUMN IF EXISTS updated_at;

ALTER TABLE sites DROP COLUMN IF EXISTS moderation_generation;
