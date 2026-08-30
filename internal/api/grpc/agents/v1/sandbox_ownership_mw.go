package v1

import (
	"context"
	"net/http"
	"time"

	"github.com/gorilla/mux"

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

		// Tenant must already be established by upstream auth. Fail closed.
		tenantID, err := s.resolveTenantID(r.Context(), "")
		if err != nil || tenantID == "" {
			writeJSONError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		vars := mux.Vars(r)
		inst, ok := s.resolveOwnedSandbox(r.Context(), vars, tenantID)
		if !ok {
			// Do not distinguish "does not exist" from "not yours".
			writeJSONError(w, http.StatusNotFound, "sandbox not found")
			return
		}

		ctx := context.WithValue(r.Context(), resolvedSandboxCtxKey{}, inst)
		next.ServeHTTP(w, r.WithContext(ctx))
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
			logger.WithFields("session_id", sid, "caller_tenant", tenantID, "sandbox_tenant", inst.Config.TenantID).
				Warn("RequireSandboxOwnership: session tenant mismatch; refusing")
			return nil, false
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
