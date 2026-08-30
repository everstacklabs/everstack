package storageauth

import (
	"context"
	"errors"
	"fmt"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/pkg/authz"
)

var (
	// ErrUnauthenticated means no verified tenant identity was available.
	ErrUnauthenticated = errors.New("storage authentication required")
	// ErrPermissionDenied means the verified principal cannot perform the
	// requested storage action or cannot act for the requested tenant.
	ErrPermissionDenied = errors.New("storage permission denied")
)

type systemPrincipalKey struct{}

// WithSystemPrincipal marks trusted background work as a tenant-scoped storage
// principal. It is deliberately separate from the generic internal-call marker
// so unrelated internal code does not bypass storage authorization.
func WithSystemPrincipal(ctx context.Context, tenantID string) context.Context {
	ctx = contextkeys.WithTenantID(ctx, tenantID)
	ctx = contextkeys.WithTenantAuthenticated(ctx)
	return context.WithValue(ctx, systemPrincipalKey{}, true)
}

func isSystemPrincipal(ctx context.Context) bool {
	principal, _ := ctx.Value(systemPrincipalKey{}).(bool)
	return principal
}

// Authorize applies the explicit server-side rule for a storage action and
// returns the verified tenant identity. Unknown actions fail closed.
func Authorize(ctx context.Context, action Action) (string, error) {
	if !contextkeys.IsTenantAuthenticated(ctx) {
		return "", fmt.Errorf("%w for %s", ErrUnauthenticated, action)
	}

	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return "", fmt.Errorf("%w: tenant identity missing for %s", ErrUnauthenticated, action)
	}

	permission, ok := PermissionFor(action)
	if !ok {
		return "", fmt.Errorf("%w: action %q has no authorization rule", ErrPermissionDenied, action)
	}

	if isSystemPrincipal(ctx) {
		return tenantID, nil
	}

	if role := contextkeys.GetUserRole(ctx); role != "" {
		if !authz.Can(authz.Role(role), permission) {
			return "", fmt.Errorf("%w: permission %s required for %s", ErrPermissionDenied, permission, action)
		}
		return tenantID, nil
	}

	// A hash is installed only after a tenant API key has been validated. The
	// authenticated marker alone is intentionally not a credential.
	if contextkeys.GetAPIKeyHash(ctx) == "" {
		return "", fmt.Errorf("%w: verified principal missing for %s", ErrPermissionDenied, action)
	}

	return tenantID, nil
}

// AuthorizeTenant additionally proves that a lower-layer tenant argument
// matches the verified request or system tenant.
func AuthorizeTenant(ctx context.Context, action Action, requestedTenantID string) (string, error) {
	tenantID, err := Authorize(ctx, action)
	if err != nil {
		return "", err
	}
	if requestedTenantID == "" || requestedTenantID != tenantID {
		return "", fmt.Errorf("%w: requested tenant is not authorized for %s", ErrPermissionDenied, action)
	}
	return tenantID, nil
}
