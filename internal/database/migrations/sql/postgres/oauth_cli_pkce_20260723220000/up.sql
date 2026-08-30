CREATE TABLE IF NOT EXISTS oauth_authorization_codes (
    id UUID PRIMARY KEY,
    code_hash TEXT NOT NULL UNIQUE,
    client_id TEXT NOT NULL,
    redirect_uri TEXT NOT NULL,
    scope TEXT NOT NULL,
    code_challenge TEXT NOT NULL,
    user_id TEXT NOT NULL,
    user_email TEXT NOT NULL DEFAULT '',
    org_id TEXT NOT NULL,
    org_slug TEXT NOT NULL DEFAULT '',
    instance_id TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_oauth_authorization_codes_expires_at
    ON oauth_authorization_codes(expires_at);

CREATE TABLE IF NOT EXISTS oauth_refresh_tokens (
    id UUID PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE,
    family_id UUID NOT NULL,
    client_id TEXT NOT NULL,
    scope TEXT NOT NULL,
    user_id TEXT NOT NULL,
    user_email TEXT NOT NULL DEFAULT '',
    org_id TEXT NOT NULL,
    org_slug TEXT NOT NULL DEFAULT '',
    instance_id TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    rotated_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    replaced_by_hash TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_oauth_refresh_tokens_family_id
    ON oauth_refresh_tokens(family_id);
CREATE INDEX IF NOT EXISTS idx_oauth_refresh_tokens_expires_at
    ON oauth_refresh_tokens(expires_at);
