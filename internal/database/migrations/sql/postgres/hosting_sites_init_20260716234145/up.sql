-- sites: one row per hosted site (evs.run instant hosting).
-- tenant_id is NULL for anonymous (unclaimed) sites; those expire via expires_at.
CREATE TABLE IF NOT EXISTS sites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug TEXT NOT NULL,
    tenant_id UUID,
    owner_user_id UUID,
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    spa_fallback BOOLEAN NOT NULL DEFAULT FALSE,
    access VARCHAR(16) NOT NULL DEFAULT 'public',
    current_version INTEGER,
    manifest_key TEXT,
    total_bytes BIGINT NOT NULL DEFAULT 0,
    file_count INTEGER NOT NULL DEFAULT 0,
    created_ip INET,
    claim_token_hash TEXT,
    claimed_at TIMESTAMPTZ,
    kill_switch BOOLEAN NOT NULL DEFAULT FALSE,
    takedown_reason TEXT,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_published_at TIMESTAMPTZ,
    CONSTRAINT uq_sites_slug UNIQUE(slug),
    CONSTRAINT ck_sites_slug_lower CHECK (slug = lower(slug))
);

CREATE INDEX idx_sites_tenant_id ON sites(tenant_id) WHERE tenant_id IS NOT NULL;
CREATE INDEX idx_sites_expires_at ON sites(expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX idx_sites_status ON sites(status);

-- site_versions: one row per publish; finalize flips status to 'finalized'
-- and points sites.current_version at it.
CREATE TABLE IF NOT EXISTS site_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    spa_fallback BOOLEAN NOT NULL DEFAULT FALSE,
    access VARCHAR(16) NOT NULL DEFAULT 'public',
    manifest_key TEXT,
    total_bytes BIGINT NOT NULL DEFAULT 0,
    file_count INTEGER NOT NULL DEFAULT 0,
    finalize_token_hash TEXT,
    created_ip INET,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finalized_at TIMESTAMPTZ,
    CONSTRAINT uq_site_versions_site_version UNIQUE(site_id, version)
);

CREATE INDEX idx_site_versions_site_id ON site_versions(site_id);
CREATE INDEX idx_site_versions_status ON site_versions(status);

-- site_files: registered before presigning; finalize verifies each object
-- landed in R2 before the manifest pointer swap.
CREATE TABLE IF NOT EXISTS site_files (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    version_id UUID NOT NULL REFERENCES site_versions(id) ON DELETE CASCADE,
    path TEXT NOT NULL,
    r2_key TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes BIGINT NOT NULL,
    sha256 CHAR(64),
    uploaded BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_site_files_version_path UNIQUE(version_id, path)
);

CREATE INDEX idx_site_files_version_id ON site_files(version_id);

-- site_email_codes: hashed one-time codes for the claim / key-issuance flow.
CREATE TABLE IF NOT EXISTS site_email_codes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email TEXT NOT NULL,
    code_hash TEXT NOT NULL,
    slug TEXT,
    ip INET,
    attempts INTEGER NOT NULL DEFAULT 0,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_site_email_codes_email_lower CHECK (email = lower(email))
);

CREATE INDEX idx_site_email_codes_email ON site_email_codes(email);
CREATE INDEX idx_site_email_codes_expires_at ON site_email_codes(expires_at);

-- site_abuse_reports: takedown queue for reported sites.
CREATE TABLE IF NOT EXISTS site_abuse_reports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug TEXT NOT NULL,
    reporter_ip INET,
    reason TEXT NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'open',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at TIMESTAMPTZ
);

CREATE INDEX idx_site_abuse_reports_slug ON site_abuse_reports(slug);
CREATE INDEX idx_site_abuse_reports_status ON site_abuse_reports(status);
