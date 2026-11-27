package v1

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

// resolvedSandboxCtxKey is the context key under which RequireSandboxOwnership
// stashes the resolved, tenant-verified instance so downstream handlers can
// reuse it instead of re-resolving (which would otherwise re-open the
// unscoped name-collision path).
type resolvedSandboxCtxKey struct{}

// ResolvedSandboxFromContext returns the instance that RequireSandboxOwnership
// resolved and verified for the current request, if any. Handlers behind the
// middleware should prefer this over calling the unscoped resolve helpers.
func ResolvedSandboxFromContext(ctx context.Context) (*sandbox.Instance, bool) {
	inst, ok := ctx.Value(resolvedSandboxCtxKey{}).(*sandbox.Instance)
	return inst, ok
}

// RequireSandboxOwnership is an HTTP middleware that enforces tenant ownership
// of the sandbox addressed by the request path BEFORE the handler runs.
//
// It must be installed AFTER an auth middleware that populates the tenant on
// the request context (the API-key interceptor for CLI/SDK callers; the cookie
// session middleware for the admin UI). It then resolves the sandbox named by
// the {sandbox_id} or {session_id} path var SCOPED to the caller's tenant, and
// returns 404 if no sandbox with that id/name/session exists in the caller's
// tenant.
//
// In cloud multi-instance mode the context tenant_id is the instance_id, so
// this enforces instance isolation; in self-hosted it is the org id. It closes
// the cross-tenant/cross-instance name-collision vector (GetBySandboxIDOrName
// is "first running wins" across every tenant in the shared gateway process).
//
// 404 (not 403) is returned on mismatch so the response does not confirm the
// existence of another tenant's sandbox.
//
// This middleware does NOT cover the shell-family routes, which authenticate
// via SSH signature / same-origin and prove tenant a different way.
func (s *Server) RequireSandboxOwnership(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.sandboxMgr == nil {
			sandboxNotEnabled(w)
			return
		}

		// A verified principal must already be established by upstream auth.
		// Fail closed.
		tenantID, ok := s.requireVerifiedTenantHTTP(w, r)
		if !ok {
			return
		}

		vars := mux.Vars(r)
		inst, ok := s.resolveOwnedSandbox(r.Context(), vars, tenantID)
		if !ok {
			// Do not distinguish "does not exist" from "not yours".
			writeJSONError(w, http.StatusNotFound, "sandbox not found")
			return
		}

		// Hand the handler the sandbox that was checked, not the string the
		// caller sent. Handlers used to look the path value up again without
		// tenant scope, where an exact id beats a name and a name is "first
		// running wins" across every tenant: a caller who named a sandbox
		// after someone else's id passed this check on their own sandbox and
		// then operated on the other one. The path vars are rewritten to the
		// checked sandbox's own id and session, and resolveSandbox returns
		// the instance stored here.
		// A {session_id} var is left alone: it is an exact map key, and
		// resolveOwnedSandbox compared the tenant of the instance it maps to.
		if _, has := vars["sandbox_id"]; has {
			vars["sandbox_id"] = inst.ID
			r = mux.SetURLVars(r, vars)
		}
		// Handlers reach the guest by session id. Refuse if that session id
		// maps, in this process, to a sandbox of a different tenant than the
		// one just checked, so the check and the use cannot diverge.
		if !s.sandboxSessionMatchesTenant(inst) {
			logger.WithFields("sandbox_id", inst.ID, "tenant_id", tenantID).
				Warn("RequireSandboxOwnership: session id maps to another tenant's sandbox; refusing")
			writeJSONError(w, http.StatusNotFound, "sandbox not found")
			return
		}
		ctx := context.WithValue(r.Context(), resolvedSandboxCtxKey{}, inst)
		if vars["sandbox_id"] != "" {
			vars["sandbox_id"] = inst.ID
			r = mux.SetURLVars(r, vars)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// sandboxSessionMatchesTenant reports whether the session id of a checked
// sandbox still leads to a sandbox of the same tenant. Session ids can be
// chosen by the caller at create time, and most guest operations are keyed by
// session id, so an ownership check on the sandbox id is only meaningful if
// its session id does not resolve to someone else's sandbox.
func (s *Server) sandboxSessionMatchesTenant(inst *sandbox.Instance) bool {
	if inst == nil || s.sandboxMgr == nil || inst.Config.SessionID == "" {
		return true
	}
	other, ok := s.sandboxMgr.GetInstance(inst.Config.SessionID)
	if !ok || other == nil {
		return true
	}
	// An instance rehydrated after a restart can be in memory before its
	// tenant has been backfilled. An empty tenant is no evidence of another
	// owner, so only a different, known tenant is a mismatch.
	if other.Config.TenantID == "" {
		return true
	}
	return other.Config.TenantID == inst.Config.TenantID
}

// errSessionIDUnavailable is returned when a caller asks to create a sandbox
// under a session id that is already assigned.
var errSessionIDUnavailable = errors.New("session_id is not available")

// sessionIDAvailableToTenant refuses a sandbox create whose session id is
// already assigned. The response is identical regardless of its owner. This
// API boundary check keeps session identity ownership separate from runtime
// sandbox reuse.
//
// Both the in-memory map and the database are consulted. A failed lookup
// refuses the create.
func (s *Server) sessionIDAvailableToTenant(ctx context.Context, sessionID, tenantID string) error {
	if s.sandboxMgr == nil || sessionID == "" {
		return nil
	}
	if tenantID == "" {
		return errSessionIDUnavailable
	}
	if inst, ok := s.sandboxMgr.GetInstance(sessionID); ok && inst != nil {
		return errSessionIDUnavailable
	}
	if s.sandboxMgr.DB() == nil {
		return nil
	}
	dbCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// Ask the tenant_id column, across every row for the session id, rather
	// than the tenant recorded inside the config JSON of the latest row.
	var taken bool
	err := s.sandboxMgr.DB().GetContext(dbCtx, &taken,
		`SELECT EXISTS (
			SELECT 1 FROM sandbox_instances
			WHERE session_id = $1 AND tenant_id <> ''
		)`, sessionID)
	if err != nil {
		logger.WithFields("tenant_id", tenantID, "error", err.Error()).
			Warn("sandbox create: session ownership lookup failed; refusing")
		return errSessionIDUnavailable
	}
	if taken {
		return errSessionIDUnavailable
	}
	return nil
}

// ownedSandboxHTTP is the in-handler form of RequireSandboxOwnership: it
// requires a verified principal, resolves sandboxID (id, name or short code)
// inside the caller's tenant and returns that instance. Callers must use the
// returned instance's ID from then on, never the string from the request.
// Writes 401 or 404 and returns false on failure; 404 (not 403) so the
// response does not confirm that another tenant's sandbox exists.
func (s *Server) ownedSandboxHTTP(w http.ResponseWriter, r *http.Request, sandboxID string) (*sandbox.Instance, bool) {
	tenantID, ok := s.requireVerifiedTenantHTTP(w, r)
	if !ok {
		return nil, false
	}
	if s.sandboxMgr == nil || sandboxID == "" {
		writeJSONError(w, http.StatusNotFound, "sandbox not found")
		return nil, false
	}
	scope := sandbox.TenantInstanceScope{TenantID: tenantID}
	if inst, found := s.sandboxMgr.GetBySandboxIDOrNameInScope(sandboxID, scope); found && inst != nil {
		return inst, true
	}
	dbCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if inst, err := s.sandboxMgr.LookupInstanceByIDFromDBInScope(dbCtx, sandboxID, scope); err == nil && inst != nil {
		return inst, true
	}
	writeJSONError(w, http.StatusNotFound, "sandbox not found")
	return nil, false
}

// requireVerifiedTenantHTTP returns the caller's tenant only when the request
// carries a verified principal: a validated session cookie, API key or CLI
// device token. A tenant id in the context is NOT enough on its own. Routes
// registered directly on the router sit outside the /v1 auth chain, and the
// standalone local-tenant middleware puts a tenant on every request, including
// ones that present no credential at all. Writes a 401 and returns false when
// the caller is not verified.
func (s *Server) requireVerifiedTenantHTTP(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !contextkeys.HasVerifiedTenantPrincipal(r.Context()) {
		writeJSONError(w, http.StatusUnauthorized, "authentication required")
		return "", false
	}
	tenantID, err := s.resolveTenantID(r.Context(), "")
	if err != nil || tenantID == "" {
		writeJSONError(w, http.StatusUnauthorized, "authentication required")
		return "", false
	}
	return tenantID, true
}

// RequireVerifiedTenant is an HTTP middleware for tenant-wide routes that are
// registered directly on the router and name no sandbox in their path
// (snapshots, lifecycle webhooks, OTLP settings, image builds, the lifecycle
// event stream). It refuses any request without a verified principal, so the
// handler's own tenant scoping always runs for a real caller.
func (s *Server) RequireVerifiedTenant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.requireVerifiedTenantHTTP(w, r); !ok {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// resolveOwnedSandbox resolves the sandbox addressed by the request path vars,
// scoped to tenantID. It prefers the concrete {sandbox_id} route, then falls
// back to {session_id}. Returns false if no matching sandbox is owned by the
// tenant.
func (s *Server) resolveOwnedSandbox(ctx context.Context, vars map[string]string, tenantID string) (*sandbox.Instance, bool) {
	dbCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	if id := vars["sandbox_id"]; id != "" {
		if inst, ok := s.sandboxMgr.GetBySandboxIDOrNameScoped(id, tenantID); ok {
			return inst, true
		}
		if inst, err := s.sandboxMgr.LookupInstanceByIDFromDBScoped(dbCtx, id, tenantID); err == nil && inst != nil {
			return inst, true
		}
		return nil, false
	}

	if sid := vars["session_id"]; sid != "" {
		if inst, ok := s.sandboxMgr.GetInstance(sid); ok {
			if inst.Config.TenantID == tenantID {
				return inst, true
			}
			if inst.Config.TenantID != "" {
				logger.WithFields("session_id", sid, "caller_tenant", tenantID, "sandbox_tenant", inst.Config.TenantID).
					Warn("RequireSandboxOwnership: session tenant mismatch; refusing")
				return nil, false
			}
		}
		if inst, err := s.sandboxMgr.LookupInstanceBySession(dbCtx, sid); err == nil && inst != nil && inst.Config.TenantID == tenantID {
			return inst, true
		}
		return nil, false
	}

	// No sandbox identifier in the path — nothing to scope. The middleware
	// should only be mounted on routes that carry one; treat a missing id as
	// not-found rather than silently allowing through.
	return nil, false
}
