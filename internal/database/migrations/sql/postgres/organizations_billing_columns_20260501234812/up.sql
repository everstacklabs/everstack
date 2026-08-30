-- The services-bundle DB's `organizations` table (created in
-- auth_self_hosted_20260111120000) was provisioned with only the bare
-- identity columns: id, slug, name, created_at, updated_at. The auth
-- repo (internal/auth/selfhosted/repository/organization_repo.go and
-- services/auth/internal/repository/organization_repo.go) selects
-- plan_tier, billing_email, stripe_customer_id, paid_seats, settings,
-- database_config off this table. On databases that haven't been
-- updated, every GetSession that takes the EnsureUserHasOrganization
-- fallback errors with:
--
--   ERROR: column "plan_tier" does not exist (SQLSTATE 42703)
--
-- and the FE's useSession returns an authenticated user with
-- organizations=[]. useOrganizationId() then returns ''. Every
-- sandbox write (CreateSandbox, RecreateSandbox) reaches the gateway
-- with tenantId='', resolveTenantID's self-hosted fallback returns
-- empty (because EnsureUserHasOrganization itself failed earlier),
-- and the RPC errors with InvalidArgument: tenant_id is required.
--
-- Net effect for the user: clicking Create does nothing, page shows
-- "Sandbox runtime is not available", no clear error anywhere.
--
-- This migration mirrors services/cloud/.../organizations_ensure_billing_columns_20260501162143/up.sql
-- but targets the services-bundle DB (no `everstack.` schema
-- prefix — that schema is only on the cloud DB).
--
-- Idempotent — safe to apply on databases that already have the
-- columns. Down migration drops only what this migration added.

ALTER TABLE organizations
    ADD COLUMN IF NOT EXISTS plan_tier VARCHAR(50) NOT NULL DEFAULT 'free';

ALTER TABLE organizations
    ADD COLUMN IF NOT EXISTS billing_email VARCHAR(255);

ALTER TABLE organizations
    ADD COLUMN IF NOT EXISTS stripe_customer_id VARCHAR(255);

ALTER TABLE organizations
    ADD COLUMN IF NOT EXISTS database_config JSONB NOT NULL DEFAULT '{}';

ALTER TABLE organizations
    ADD COLUMN IF NOT EXISTS settings JSONB NOT NULL DEFAULT '{}';

ALTER TABLE organizations
    ADD COLUMN IF NOT EXISTS paid_seats INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_organizations_stripe_customer
    ON organizations(stripe_customer_id);
