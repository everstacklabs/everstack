-- Auto-archive and auto-delete lifecycle tiers for sandbox_instances.
--
-- auto_archive_after_days: when a stopped (sleeping) sandbox has been idle
--   for this many days, the ArchiveChecker transitions it to desired_state='archived'.
--   0 = disabled (never auto-archive). Default: 7 days.
--
-- auto_delete_after_days: when a sandbox (in any stopped/archived state) has been
--   in that state for this many days, the ArchiveChecker sets desired_state='terminated'.
--   -1 = disabled (never auto-delete). 0 = delete immediately on stop. Default: -1 (disabled).
--
-- archived_at: the wall-clock time when the row entered lifecycle_state='archived'.
--   Used by the ArchiveChecker to compute auto_delete eligibility for archived rows.

ALTER TABLE sandbox_instances
    ADD COLUMN IF NOT EXISTS auto_archive_after_days INT     NOT NULL DEFAULT 7,
    ADD COLUMN IF NOT EXISTS auto_delete_after_days  INT     NOT NULL DEFAULT -1,
    ADD COLUMN IF NOT EXISTS archived_at             TIMESTAMPTZ;

-- Partial index for the ArchiveChecker's eligibility query.
-- Covers sleeping rows past their archive threshold and archived rows
-- past their delete threshold, without scanning non-eligible rows.
CREATE INDEX IF NOT EXISTS idx_sandbox_instances_archive_due
    ON sandbox_instances (lifecycle_state, stopped_at, auto_archive_after_days)
    WHERE lifecycle_state = 'sleeping'
      AND auto_archive_after_days > 0
      AND stopped_at IS NOT NULL;
