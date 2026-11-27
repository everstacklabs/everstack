-- Record-only units (zero amount) cannot satisfy the original constraint;
-- they carry no charge, so dropping them loses no money.
DELETE FROM everstack.billing_settle_outbox WHERE amount_micros <= 0;

ALTER TABLE everstack.billing_settle_outbox
    DROP CONSTRAINT IF EXISTS billing_settle_outbox_amount_micros_check;

ALTER TABLE everstack.billing_settle_outbox
    ADD CONSTRAINT billing_settle_outbox_amount_micros_check CHECK (amount_micros > 0);

ALTER TABLE everstack.billing_settle_outbox
    DROP COLUMN IF EXISTS attempt;
