-- Sandbox lifecycle unification (Daytona-parity phase 1).
--
-- The desired-state reconciler becomes the SOLE lifecycle driver; the
-- legacy gateway claim cascades and the 60s reaper are being retired.
-- This migration:
--   1. Adds the recoverable error state plumbing (error_reason/error_at).
--      'failed' remains as a legacy value; new failures write 'error'.
--   2. Adds Daytona-style minute-granularity auto intervals that
--      supersede idle_retention_secs / auto_archive_after_days /
--      auto_delete_after_days (old columns kept for one release for
--      rollback; readers move to the new ones).
--        auto_stop_minutes    NULL = plan-tier default, 0 = disabled
--        auto_archive_minutes 0 = disabled
--        auto_delete_minutes  -1 = never, 0 = ephemeral (delete on stop)
--   3. Folds the legacy 'stopped' lifecycle vocabulary into 'sleeping'
--      so SetDesiredState's transition CASE applies uniformly.
--   4. Backfills desired_state so every pre-reconciler row is coherent.
--   5. Rebuilds the reconcile-due index to include 'archiving', which
--      was previously unclaimable (archive requests wedged forever).
--   6. Adds workspace_archive_ref: the R2 object key once a sandbox's
--      workspace tarball has been moved to object storage (archived).

ALTER TABLE sandbox_instances
    ADD COLUMN IF NOT EXISTS error_reason          TEXT,
    ADD COLUMN IF NOT EXISTS error_at              TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS auto_stop_minutes     INT,
    ADD COLUMN IF NOT EXISTS auto_archive_minutes  INT,
    ADD COLUMN IF NOT EXISTS auto_delete_minutes   INT,
    ADD COLUMN IF NOT EXISTS workspace_archive_ref TEXT;

-- (3) Vocabulary unification: legacy gateway wrote 'stopped'; the
-- reconciler writes 'sleeping'. Rows in 'stopped' could never be
-- revived or archived through SetDesiredState (its CASE has no
-- 'stopped' arm). stopped_at is required by the archive/delete sweeps.
UPDATE sandbox_instances
SET lifecycle_state = 'sleeping',
    stopped_at      = COALESCE(stopped_at, updated_at)
WHERE lifecycle_state = 'stopped';

-- (4) desired_state backfill for rows that predate the reconciler or
-- were driven by the legacy path while the flag was off.
UPDATE sandbox_instances
SET desired_state = CASE
        WHEN lifecycle_state IN ('sleeping', 'stopping')                THEN 'sleeping'
        WHEN lifecycle_state IN ('archived', 'archiving')               THEN 'archived'
        WHEN lifecycle_state IN ('terminated', 'terminating', 'failed') THEN 'terminated'
        ELSE 'running'
    END
WHERE desired_state IS DISTINCT FROM CASE
        WHEN lifecycle_state IN ('sleeping', 'stopping')                THEN 'sleeping'
        WHEN lifecycle_state IN ('archived', 'archiving')               THEN 'archived'
        WHEN lifecycle_state IN ('terminated', 'terminating', 'failed') THEN 'terminated'
        ELSE 'running'
    END;

-- (2) Minutes backfill from the day/second-granularity predecessors.
-- idle_retention_secs: 0 meant "never auto-stop" in the legacy reaper,
-- so it maps to 0 (disabled) here, NOT to the tier default (NULL).
UPDATE sandbox_instances
SET auto_archive_minutes = CASE
        WHEN COALESCE(auto_archive_after_days, 0) > 0 THEN auto_archive_after_days * 1440
        ELSE 0
    END,
    auto_delete_minutes = CASE
        WHEN COALESCE(auto_delete_after_days, -1) >= 0 THEN auto_delete_after_days * 1440
        ELSE -1
    END,
    auto_stop_minutes = CASE
        WHEN idle_retention_secs IS NULL     THEN NULL
        WHEN idle_retention_secs <= 0        THEN 0
        ELSE GREATEST(1, idle_retention_secs / 60)
    END
WHERE auto_archive_minutes IS NULL
  AND auto_delete_minutes IS NULL
  AND auto_stop_minutes IS NULL;

-- (5) The reconciler's "find me work" index must cover 'archiving' or
-- ClaimDue can never pick those rows up.
DROP INDEX IF EXISTS idx_sandbox_instances_reconcile_due;
CREATE INDEX idx_sandbox_instances_reconcile_due
    ON sandbox_instances (reconcile_after)
    WHERE lifecycle_state IN ('pending', 'creating', 'stopping', 'reviving', 'archiving', 'terminating');

-- Error rows are surfaced in lists and swept by the delete checker via
-- error_at; keep them cheap to find.
CREATE INDEX IF NOT EXISTS idx_sandbox_instances_error
    ON sandbox_instances (error_at)
    WHERE lifecycle_state IN ('error', 'failed');
