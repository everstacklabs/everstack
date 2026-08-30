-- Per-tenant launch-center onboarding state. One row per tenant.
--
-- tenant_id is intentionally TEXT, not UUID: the value source is always
-- contextkeys.GetTenantID / EVS_ORG_ID (self-consistent, no FK to orgs), and
-- TEXT sidesteps an implicit UUID cast that cannot be verified against a real
-- Postgres in this environment.
CREATE TABLE IF NOT EXISTS onboarding_state (
    tenant_id         TEXT PRIMARY KEY,
    dismissed         BOOLEAN NOT NULL DEFAULT FALSE,
    celebration_shown BOOLEAN NOT NULL DEFAULT FALSE,
    selected_path     VARCHAR(32) NOT NULL DEFAULT '',
    sandbox_skipped   BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
