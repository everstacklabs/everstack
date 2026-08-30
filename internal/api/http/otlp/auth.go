package otlp

import (
	"net/http"

	"github.com/everstacklabs/everstack/internal/database"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// TenantResolver resolves an inbound OTLP request to a tenant ID from its
// Everstack API key. Satisfied by *mcpserverauth.APIKeyAuthenticator.
//
// PresentsCredential reports whether a key was supplied at all, regardless of
// validity. It belongs on this interface rather than being re-derived from the
// headers here, so that "what counts as a presented credential" can never drift
// from what Authenticate actually reads.
type TenantResolver interface {
	Authenticate(r *http.Request) (string, bool)
	PresentsCredential(r *http.Request) bool
}

// WithTenantAuth resolves the tenant for an OTLP ingest request and injects it
// into the request context so the downstream receiver (and its tenant-stamped
// inserts) see a real tenant.
//
// Why this exists: the OTLP receivers are mounted on the shared gateway router,
// where the api-key interceptor rejects bare Bearer tokens. So we authenticate
// exactly the way the inbound MCP server does — resolve the tenant directly from
// the presented Everstack API key (Authorization: Bearer <key> /
// x-everstack-api-key), independent of host-based tenant resolution.
//
// The API key is resolved FIRST and is authoritative for who owns the ingested
// data. We must NOT trust a tenant that an upstream middleware put in the
// context: on a shared/managed gateway LocalScopeResolver injects a fallback
// tenant (the 00000000-…0001 self-hosted default, or whatever
// system.instances.local_instance_id happens to hold) into every request, and
// honoring it would mis-attribute spans to the wrong tenant so they never show
// up under the real tenant in the dashboard. The context tenant is only used as
// a fallback when no API key is present at all (self-hosted standalone, where
// LocalScopeResolver legitimately injects the real local tenant id).
//
// A key that is PRESENTED but does not resolve is rejected outright, never
// falls back. That distinction is the whole point: a revoked, mistyped, or
// foreign-instance key used to slide into the fallback branch and ingest under
// the local placeholder tenant. The client saw 200, the spans were written, and
// nothing could ever read them back because the dashboard queries the real
// tenant. Silent, indefinite, and indistinguishable from "OTel is broken".
func WithTenantAuth(next http.Handler, auth TenantResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API key first — authoritative owner of the ingested data.
		if auth != nil {
			if tenantID, ok := auth.Authenticate(r); ok && tenantID != "" {
				ctx := contextkeys.WithTenantID(r.Context(), tenantID)
				ctx = database.WithTenantSchema(ctx, tenantID)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			if auth.PresentsCredential(r) {
				reject(w)
				return
			}
		}
		// No credential at all — fall back to a tenant resolved upstream
		// (self-hosted standalone mode).
		if contextkeys.GetTenantID(r.Context()) != "" || database.TenantSchemaFromContext(r.Context()) != "" {
			next.ServeHTTP(w, r)
			return
		}
		reject(w)
	})
}

func reject(w http.ResponseWriter) {
	http.Error(w, "unauthorized: provide a valid Everstack API key as 'Authorization: Bearer <key>'", http.StatusUnauthorized)
}
