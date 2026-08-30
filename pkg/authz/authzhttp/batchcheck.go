// Package authzhttp exposes the authorization engine to the frontend PDP via a
// BatchCheck endpoint. The UI sends the (permission, object) pairs it wants to
// gate on; the backend answers using the SAME engine it enforces with, so the
// UI cannot drift from enforcement. The response shape matches the TS
// PermissionSet ("permission" or "permission@object").
package authzhttp

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/everstacklabs/everstack/pkg/authz"
)

// Handler serves BatchCheck.
type Handler struct {
	engine      *authz.Engine
	userID      func(ctx context.Context) string
	tenant      func(ctx context.Context) string
	sessionRole func(ctx context.Context) string
}

// New builds the handler. userID extracts the verified caller from context;
// tenant scopes every check to the caller's tenant (the Postgres tuple store
// fails closed without it); sessionRole (optional) supplies the caller's
// org-level role so instance-local resource checks resolve via the bridge.
func New(engine *authz.Engine, userID, tenant, sessionRole func(ctx context.Context) string) *Handler {
	return &Handler{engine: engine, userID: userID, tenant: tenant, sessionRole: sessionRole}
}

type checkItem struct {
	Permission string `json:"permission"`
	Object     string `json:"object,omitempty"` // "type:id"; required for resource/org-scoped checks
}

type batchRequest struct {
	Checks []checkItem `json:"checks"`
}

type batchResponse struct {
	Granted []string `json:"granted"` // entries the caller IS allowed: "perm" or "perm@object"
}

// BatchCheck handles POST {checks:[{permission,object}]} and returns the subset
// the caller is granted, as PermissionSet keys.
func (h *Handler) BatchCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	userID := ""
	if h.userID != nil {
		userID = h.userID(r.Context())
	}
	if userID == "" {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	var req batchRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	tenant := ""
	if h.tenant != nil {
		tenant = h.tenant(ctx)
		ctx = authz.ContextWithTenant(ctx, tenant)
	}
	if h.sessionRole != nil && userID != "" && tenant != "" {
		if role := authz.Role(h.sessionRole(ctx)); role.Valid() {
			ctx = authz.ContextWithSessionMembership(ctx, authz.SessionMembership{
				UserID: userID, Tenant: tenant, Role: role,
			})
		}
	}

	granted := make([]string, 0, len(req.Checks))
	for _, c := range req.Checks {
		if c.Object == "" {
			continue // object-scoped checks only; no ambient grants
		}
		obj, err := authz.ParseObject(c.Object)
		if err != nil {
			continue
		}
		ok, err := h.engine.CheckPermission(ctx, userID, authz.Permission(c.Permission), obj)
		if err != nil || !ok {
			continue
		}
		granted = append(granted, c.Permission+"@"+c.Object)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(batchResponse{Granted: granted})
}
