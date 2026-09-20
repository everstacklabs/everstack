package openaiapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/auth/deviceauth"
	"github.com/everstacklabs/everstack/internal/database"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/jmoiron/sqlx"
)

// WithDeviceTokenAuth accepts only Everstack's signed OAuth/device token. It
// rechecks current organization membership and instance ownership before
// installing request identity, so a valid but revoked or moved token fails
// closed. Other auth mechanisms pass through to the standard API-key/session
// middleware.
func WithDeviceTokenAuth(next http.Handler, tokens *deviceauth.TokenManager, db *sqlx.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(auth, "Bearer ") {
			next.ServeHTTP(w, r)
			return
		}
		if tokens == nil || db == nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		identity, err := tokens.Verify(strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")))
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		tenantID := identity.OrganizationID
		requestCtx := r.Context()
		hostScope, hasHostScope := contextkeys.RequestInstanceScopeFromContext(requestCtx)
		if hasHostScope && hostScope.OrganizationID != identity.OrganizationID {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if hasHostScope && identity.InstanceID != "" && hostScope.InstanceID != identity.InstanceID {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if hasHostScope && identity.InstanceID == "" {
			identity.InstanceID = hostScope.InstanceID
		}
		if !hasHostScope && identity.InstanceID == "" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		type membership struct {
			Role   string `db:"role"`
			UserID string `db:"user_id"`
		}
		var member membership
		err = db.GetContext(ctx, &member, `SELECT COALESCE(role,'') AS role, user_id::text AS user_id FROM everstack.organization_members WHERE user_id::text=$1 AND organization_id=$2::uuid LIMIT 1`, identity.UserID, identity.OrganizationID)
		if errors.Is(err, sql.ErrNoRows) && strings.TrimSpace(identity.Email) != "" {
			// Managed identity subjects and Everstack user IDs are distinct. Resolve
			// that boundary only through a verified identity link in the signed
			// token's organization.
			member = membership{}
			err = db.GetContext(ctx, &member, `SELECT COALESCE(om.role,'') AS role, om.user_id::text AS user_id
				FROM everstack.organization_members om
				JOIN everstack.identity_links il ON il.user_id=om.user_id
				JOIN everstack.tenant_config tc ON tc.organization_id=om.organization_id
				WHERE il.email_verified=TRUE AND lower(il.email_at_link)=lower($3::text)
				  AND om.organization_id=$1::uuid
				  AND tc.instance_id::text=$2 AND tc.status='active'
				LIMIT 1`, identity.OrganizationID, identity.InstanceID, strings.TrimSpace(identity.Email))
		}
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "Forbidden", http.StatusForbidden)
			} else {
				http.Error(w, "Authorization unavailable", http.StatusServiceUnavailable)
			}
			return
		}
		if strings.TrimSpace(member.UserID) == "" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if identity.InstanceID != "" {
			var count int
			if err = db.GetContext(ctx, &count, `SELECT COUNT(*) FROM everstack.tenant_config WHERE instance_id=$1 AND organization_id=$2::uuid AND status='active'`, identity.InstanceID, identity.OrganizationID); err != nil {
				http.Error(w, "Authorization unavailable", http.StatusServiceUnavailable)
				return
			}
			if count != 1 {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			tenantID = identity.InstanceID
			if !hasHostScope {
				requestCtx = contextkeys.WithRequestInstanceScope(requestCtx, contextkeys.RequestInstanceScope{InstanceID: identity.InstanceID, OrganizationID: identity.OrganizationID, OrganizationSlug: identity.OrganizationSlug})
			}
		}
		requestCtx = contextkeys.WithUserID(requestCtx, member.UserID)
		requestCtx = contextkeys.WithCloudUserID(requestCtx, member.UserID)
		requestCtx = contextkeys.WithUserRole(requestCtx, member.Role)
		requestCtx = contextkeys.WithTenantID(requestCtx, tenantID)
		requestCtx = contextkeys.WithTenantAuthenticated(requestCtx)
		requestCtx = database.WithTenantSchema(requestCtx, tenantID)
		next.ServeHTTP(w, r.WithContext(requestCtx))
	})
}
