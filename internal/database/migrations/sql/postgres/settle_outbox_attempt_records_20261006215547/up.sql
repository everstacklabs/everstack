-- Billed inference attempts ride the settle outbox (billing.inference_attempts
-- in the platform DB). Every provider attempt is now a settle unit, including
-- the ones that are recorded but not charged (a BYOK attempt, a platform
-- retry, a failed attempt that generated no tokens), so a parked unit may
-- carry a zero amount, and the attempt record it must land is kept beside the
-- ledger metadata so replay writes the same row the request path would have.
ALTER TABLE everstack.billing_settle_outbox
    ADD COLUMN IF NOT EXISTS attempt JSONB;

ALTER TABLE everstack.billing_settle_outbox
    DROP CONSTRAINT IF EXISTS billing_settle_outbox_amount_micros_check;

ALTER TABLE everstack.billing_settle_outbox
    ADD CONSTRAINT billing_settle_outbox_amount_micros_check
    CHECK (amount_micros > 0 OR (amount_micros = 0 AND attempt IS NOT NULL));
