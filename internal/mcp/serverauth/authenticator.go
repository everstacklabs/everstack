// Package serverauth provides the API-key Authenticator for the MCP server
// (internal/mcp/server). It resolves an inbound request to a single tenant by
// hashing the presented Everstack API key and looking it up — exactly mirroring
// the gateway's api_key_interceptor, but as a directly-injected dependency.
//
// Why injected and not read from the request context: the MCP server mounts as
// a raw mux route that does NOT carry the CQRS system in its per-request
// context (CQRS injection only wraps the Connect handlers). An externally
// reachable endpoint must resolve tenant from an explicit, testable dependency,
// never from ambient state — and never via a "first/only org in the DB"
// fallback, which is the cross-tenant leak pattern we have been bitten by.
package serverauth

import (
	"context"
	"github.com/everstacklabs/everstack/internal/lib/tenantlive"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jmoiron/sqlx"

	a2aserver "github.com/everstacklabs/everstack/internal/a2a/server"
	apikeylib "github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/mcp/server"
	"github.com/everstacklabs/everstack/internal/query"
	apikey "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
)

// apiKeyLookup is the subset of the api-key query handler the authenticator
// needs. Declaring it here keeps the authenticator unit-testable with a fake.
type apiKeyLookup interface {
	Handle(ctx context.Context, q query.Query) (interface{}, error)
}

// APIKeyAuthenticator authenticates MCP requests via an Everstack API key.
type APIKeyAuthenticator struct {
	lookup  apiKeyLookup
	logger  *slog.Logger
	surface apikeylib.Surface
}

var _ server.Authenticator = (*APIKeyAuthenticator)(nil)
var _ a2aserver.Authenticator = (*APIKeyAuthenticator)(nil)

// NewAPIKeyAuthenticator builds an authenticator backed by the given database
// (the one that holds the api_keys table — the primary RW pool).
func NewAPIKeyAuthenticator(db *sqlx.DB, logger *slog.Logger) *APIKeyAuthenticator {
	return newWithLookup(apikey.NewApiKeyByHashQueryHandler(db), logger)
}

func newWithLookup(lookup apiKeyLookup, logger *slog.Logger) *APIKeyAuthenticator {
	if logger == nil {
		logger = slog.Default()
	}
	return &APIKeyAuthenticator{lookup: lookup, logger: logger.With("component", "mcp_server_auth")}
}

// Authenticate resolves the request to a tenant ID, or returns ("", false).
// It fails closed on every uncertain path.
func (a *APIKeyAuthenticator) Authenticate(r *http.Request) (string, bool) {
	tenantID, _, ok := a.AuthenticateKey(r)
	return tenantID, ok
}

// AuthenticateKey is Authenticate plus the verified key's id (api_keys.id),
// which attributes inference the request triggers to that key
// (docs/design/spend-limits-v2.md). It fails closed exactly like
// Authenticate and returns no id on failure.
func (a *APIKeyAuthenticator) AuthenticateKey(r *http.Request) (tenantID, apiKeyID string, ok bool) {
	_, tenantID, apiKeyID, ok = a.AuthenticateKeyContext(r)
	return
}

// AuthenticateKeyContext carries the verified surface set alongside the key id.
func (a *APIKeyAuthenticator) AuthenticateKeyContext(r *http.Request) (ctx context.Context, tenantID, apiKeyID string, ok bool) {
	ctx = r.Context()
	key, presented, malformed := ExtractPresentedKey(r)
	if !presented || malformed {
		return ctx, "", "", false
	}

	// Hash with the configured HMAC secret (per-tenant if present in context,
	// else the global secret). If no secret is configured we cannot safely
	// authenticate, so fail closed rather than guessing.
	hash, ok := apikeylib.HashFromContext(r.Context(), key)
	if !ok {
		a.logger.Warn("MCP auth: no API key HMAC secret configured (set server.security.api_key_hash_secret or EVS_API_KEY_HASH_SECRET); rejecting")
		return ctx, "", "", false
	}

	q := apikey.NewGetApiKeyByHashQuery(hash, "", "")
	q.Surface = a.surface
	res, err := a.lookup.Handle(r.Context(), q)
	if err != nil {
		a.logger.Warn("MCP auth: api key lookup failed", "error", err)
		return ctx, "", "", false
	}
	if res == nil {
		return ctx, "", "", false // unknown or revoked key
	}
	rm, ok := res.(apikey.APIKeyReadModel)
	if !ok {
		a.logger.Warn("MCP auth: unexpected api key lookup result type")
		return ctx, "", "", false
	}

	if !apikeylib.Allowed(rm.Surfaces, a.surface) {
		return ctx, "", "", false
	}

	// Prefer instance_id (cloud multi-instance), then org_id. No fallback to a
	// sole tenant — see the package doc.
	if rm.InstanceID != nil && strings.TrimSpace(*rm.InstanceID) != "" {
		tenantID = strings.TrimSpace(*rm.InstanceID)
	} else if rm.OrgID != nil {
		tenantID = strings.TrimSpace(*rm.OrgID)
	}
	if tenantID == "" || tenantlive.Deleted(tenantID) {
		return ctx, "", "", false // no tenant, or a deleted managed instance
	}
	// A key presented on another tenant's hostname fails like an unknown
	// key (see contextkeys.HostScopeAdmitsTenant).
	if !contextkeys.HostScopeAdmitsTenant(r.Context(), tenantID) {
		a.logger.Warn("MCP auth: key presented on another tenant's host; refused", "key_tenant_id", tenantID, "host", r.Host)
		return ctx, "", "", false
	}
	ctx = contextkeys.WithAPIKeyRateLimit(contextkeys.WithAPIKeySurfaces(ctx, rm.Surfaces), rm.RateLimitRPM)
	ctx = apikeylib.Accept(ctx, rm.ID, tenantID, rm.Surfaces, a.surface, rm.RateLimitRPM)
	return ctx, tenantID, rm.ID, true
}

// PresentsCredential reports whether the caller supplied an API key at all, in
// any of the forms Authenticate reads. It says nothing about whether that key
// is valid.
//
// Callers need the distinction because "no credential" and "a credential that
// failed" must be handled differently: the first may legitimately fall through
// to another tenant source on a standalone deployment, the second must always
// be rejected. Resolving that from the raw headers at the call site would
// duplicate credential parsing and drift from it, so the answer lives here, next to the
// function whose header list it has to match.
func (a *APIKeyAuthenticator) PresentsCredential(r *http.Request) bool {
	_, presented, _ := ExtractPresentedKey(r)
	return presented
}

// ForSurface returns an immutable view declaring the serving protocol.
func (a *APIKeyAuthenticator) ForSurface(surface apikeylib.Surface) *APIKeyAuthenticator {
	view := *a
	view.surface = surface
	return &view
}
