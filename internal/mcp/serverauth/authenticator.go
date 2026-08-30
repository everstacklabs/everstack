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
	"log/slog"
	"net/http"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/api/common"
	apikeylib "github.com/everstacklabs/everstack/internal/lib/apikey"
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
	lookup apiKeyLookup
	logger *slog.Logger
}

var _ server.Authenticator = (*APIKeyAuthenticator)(nil)

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
	key := extractKey(r)
	if key == "" {
		return "", false
	}

	// Hash with the configured HMAC secret (per-tenant if present in context,
	// else the global secret). If no secret is configured we cannot safely
	// authenticate, so fail closed rather than guessing.
	hash, ok := apikeylib.HashFromContext(r.Context(), key)
	if !ok {
		a.logger.Warn("MCP auth: no API key HMAC secret configured (set server.security.api_key_hash_secret or EVS_API_KEY_HASH_SECRET); rejecting")
		return "", false
	}

	res, err := a.lookup.Handle(r.Context(), apikey.NewGetApiKeyByHashQuery(hash, "", ""))
	if err != nil {
		a.logger.Warn("MCP auth: api key lookup failed", "error", err)
		return "", false
	}
	if res == nil {
		return "", false // unknown or revoked key
	}
	rm, ok := res.(apikey.APIKeyReadModel)
	if !ok {
		a.logger.Warn("MCP auth: unexpected api key lookup result type")
		return "", false
	}

	// Prefer instance_id (cloud multi-instance), then org_id. No fallback to a
	// sole tenant — see the package doc.
	tenantID := ""
	if rm.InstanceID != nil && strings.TrimSpace(*rm.InstanceID) != "" {
		tenantID = strings.TrimSpace(*rm.InstanceID)
	} else if rm.OrgID != nil {
		tenantID = strings.TrimSpace(*rm.OrgID)
	}
	if tenantID == "" {
		return "", false
	}
	return tenantID, true
}

// PresentsCredential reports whether the caller supplied an API key at all, in
// any of the forms Authenticate reads. It says nothing about whether that key
// is valid.
//
// Callers need the distinction because "no credential" and "a credential that
// failed" must be handled differently: the first may legitimately fall through
// to another tenant source on a standalone deployment, the second must always
// be rejected. Resolving that from the raw headers at the call site would
// duplicate extractKey and drift from it, so the answer lives here, next to the
// function whose header list it has to match.
func (a *APIKeyAuthenticator) PresentsCredential(r *http.Request) bool {
	return extractKey(r) != ""
}

// extractKey pulls the API key from the standard MCP "Authorization: Bearer"
// header, falling back to Everstack's own api-key header for clients that set
// it directly.
func extractKey(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		if k := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")); k != "" {
			return k
		}
	}
	// Canonical x-evs-api-key, falling back to legacy x-mf-api-key and the
	// x-everstack-api-key that shipped OTLP/MCP clients already send.
	if k := strings.TrimSpace(common.GetHTTPHeader(r.Header, common.EverstackApiKey, common.LegacyMFApiKey, common.LegacyEverstackApiKey)); k != "" {
		return k
	}
	return ""
}
