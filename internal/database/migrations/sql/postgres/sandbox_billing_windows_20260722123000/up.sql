-- A sandbox's durable identity can survive many stop/revive cycles. Keep the
-- current allocated-compute window separate from created_at so sleeping time
-- is never charged as CPU/memory time after a revive or gateway restart.
ALTER TABLE sandbox_instances
    ADD COLUMN IF NOT EXISTS billing_started_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS billing_ended_at TIMESTAMPTZ;

-- Existing active rows need a safe initial boundary. Prefer the most recent
-- event that proves a VM became usable; fall back to created_at for legacy
-- rows that predate lifecycle events. Stopped/archived rows remain NULL.
UPDATE sandbox_instances AS si
SET billing_started_at = COALESCE(
    (
        SELECT MAX(se.created_at)
        FROM sandbox_events AS se
        WHERE se.sandbox_id = si.id
          AND se.event_type IN ('ready', 'created', 'sandbox.revived')
    ),
    si.created_at
)
WHERE si.billing_started_at IS NULL
  AND (
      si.lifecycle_state IN ('running', 'stopping', 'terminating')
      OR (COALESCE(si.lifecycle_state, '') = '' AND si.status IN ('running', 'idle'))
  )
  AND si.destroyed_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_sandbox_instances_open_billing_window
    ON sandbox_instances (tenant_id, billing_started_at)
    WHERE billing_started_at IS NOT NULL;

COMMENT ON COLUMN sandbox_instances.billing_started_at IS
    'Start of the currently open allocated-compute billing window; NULL when no VM compute is billable. created_at remains immutable.';

COMMENT ON COLUMN sandbox_instances.billing_ended_at IS
    'Durable observed end of an allocation whose ledger close is still pending; prevents retries or outages from extending compute charges.';
