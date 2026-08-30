DROP INDEX IF EXISTS idx_organizations_stripe_customer;

ALTER TABLE organizations DROP COLUMN IF EXISTS paid_seats;
ALTER TABLE organizations DROP COLUMN IF EXISTS settings;
ALTER TABLE organizations DROP COLUMN IF EXISTS database_config;
ALTER TABLE organizations DROP COLUMN IF EXISTS stripe_customer_id;
ALTER TABLE organizations DROP COLUMN IF EXISTS billing_email;
ALTER TABLE organizations DROP COLUMN IF EXISTS plan_tier;
