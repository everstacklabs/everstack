-- Settle outbox for wallet debits (docs/design/prepaid-credits-v2.md section 5,
-- "Settle durability"). The gateway meters platform-key inference at provider
-- dispatch and debits the platform DB's billing.credit_ledger off the request
-- path. When that debit fails after in-process retries, or the shutdown drain
-- runs out of budget, the settle unit is parked here, in the gateway's own
-- primary DB, and a replay loop lands it with the SAME idempotency key until
-- billing.credit_ledger accepts it (ON CONFLICT DO NOTHING there makes the
-- replay exactly-once). Platform-level, not tenant-scoped: organization_id is
-- the billable org, not a gateway tenant, so this table is deliberately not in
-- the RLS tenant-isolation list.
CREATE TABLE IF NOT EXISTS everstack.billing_settle_outbox (
    id BIGSERIAL PRIMARY KEY,
    -- The ledger key ("inference:<settle_unit_id>"); one row per settle unit.
    idempotency_key TEXT NOT NULL UNIQUE,
    settle_unit_id TEXT NOT NULL,
    organization_id TEXT NOT NULL,
    modality TEXT NOT NULL DEFAULT 'chat',
    amount_micros BIGINT NOT NULL CHECK (amount_micros > 0),
    -- When the attempt completed; selects the ledger period on replay.
    occurred_at TIMESTAMPTZ NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS billing_settle_outbox_due_idx
    ON everstack.billing_settle_outbox (next_attempt_at);
