package v1

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/storageauth"
)

func (s *Server) authorizeStorage(ctx context.Context, action storageauth.Action) (string, error) {
	tenantID, err := storageauth.Authorize(ctx, action)
	if err == nil {
		return tenantID, nil
	}
	if errors.Is(err, storageauth.ErrUnauthenticated) {
		return "", connect.NewError(connect.CodeUnauthenticated, err)
	}
	return "", connect.NewError(connect.CodePermissionDenied, err)
}

func (s *Server) authorizeStorageTenant(ctx context.Context, action storageauth.Action, requestedTenantID string) (string, error) {
	tenantID, err := storageauth.AuthorizeTenant(ctx, action, requestedTenantID)
	if err == nil {
		return tenantID, nil
	}
	if errors.Is(err, storageauth.ErrUnauthenticated) {
		return "", connect.NewError(connect.CodeUnauthenticated, err)
	}
	return "", connect.NewError(connect.CodePermissionDenied, err)
}

func writeStorageAuthorizationError(w http.ResponseWriter, err error) {
	status := http.StatusUnauthorized
	if connect.CodeOf(err) == connect.CodePermissionDenied {
		status = http.StatusForbidden
	}
	http.Error(w, http.StatusText(status), status)
}
