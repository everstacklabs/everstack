package storageauth

import (
	"context"
	"errors"
	"testing"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/pkg/authz"
)

func TestEveryStorageActionHasAnExplicitPermission(t *testing.T) {
	expected := map[Action]authz.Permission{
		ActionConnectionConfigure: authz.PermStorageManage,
		ActionConnectionRead:      authz.PermStorageRead,
		ActionConnectionUpdate:    authz.PermStorageManage,
		ActionConnectionDelete:    authz.PermStorageManage,
		ActionUploadInitiate:      authz.PermStorageWrite,
		ActionUploadRead:          authz.PermStorageRead,
		ActionUploadComplete:      authz.PermStorageWrite,
		ActionUploadProxy:         authz.PermStorageWrite,
		ActionUploadInternal:      authz.PermStorageWrite,
		ActionObjectDownload:      authz.PermStorageRead,
		ActionObjectDelete:        authz.PermStorageWrite,
		ActionObjectList:          authz.PermStorageRead,
		ActionUsageRead:           authz.PermStorageRead,
		ActionUsageUpdate:         authz.PermStorageWrite,
		ActionWorkspaceRead:       authz.PermStorageRead,
		ActionWorkspaceWrite:      authz.PermStorageWrite,
		ActionCheckpointCreate:    authz.PermStorageWrite,
		ActionCheckpointRestore:   authz.PermStorageWrite,
		ActionWorkspaceFork:       authz.PermStorageWrite,
		ActionArtifactPromote:     authz.PermStorageWrite,
		ActionAdminSync:           authz.PermStorageManage,
		ActionAdminReconcile:      authz.PermStorageManage,
	}

	actions := AllActions()
	if len(actions) != len(expected) {
		t.Fatalf("AllActions() has %d actions, want %d", len(actions), len(expected))
	}
	for _, action := range actions {
		want, exists := expected[action]
		if !exists {
			t.Errorf("unexpected storage action %q", action)
			continue
		}
		got, ok := PermissionFor(action)
		if !ok {
			t.Errorf("PermissionFor(%q) has no rule", action)
			continue
		}
		if got != want {
			t.Errorf("PermissionFor(%q) = %q, want %q", action, got, want)
		}
	}
}

func TestUnknownStorageActionFailsClosed(t *testing.T) {
	if permission, ok := PermissionFor(Action("storage.unknown")); ok || permission != "" {
		t.Fatalf("PermissionFor(unknown) = (%q, %v), want empty false", permission, ok)
	}
}

func TestAuthorizeFailsClosedWithoutVerifiedPrincipal(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		err  error
	}{
		{
			name: "unauthenticated",
			ctx:  context.Background(),
			err:  ErrUnauthenticated,
		},
		{
			name: "unverified tenant context",
			ctx:  contextkeys.WithTenantID(context.Background(), "tenant-1"),
			err:  ErrUnauthenticated,
		},
		{
			name: "authenticated without tenant",
			ctx:  contextkeys.WithTenantAuthenticated(context.Background()),
			err:  ErrUnauthenticated,
		},
		{
			name: "roleless marker",
			ctx: contextkeys.WithTenantAuthenticated(
				contextkeys.WithTenantID(context.Background(), "tenant-1"),
			),
			err: ErrPermissionDenied,
		},
		{
			name: "unknown role",
			ctx: contextkeys.WithUserRole(
				contextkeys.WithTenantAuthenticated(
					contextkeys.WithTenantID(context.Background(), "tenant-1"),
				),
				"superadmin",
			),
			err: ErrPermissionDenied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Authorize(tt.ctx, ActionConnectionRead); !errors.Is(err, tt.err) {
				t.Fatalf("Authorize() error = %v, want %v", err, tt.err)
			}
		})
	}
}

func TestAuthorizeAppliesActionPermissions(t *testing.T) {
	viewerCtx := contextkeys.WithUserRole(
		contextkeys.WithTenantAuthenticated(
			contextkeys.WithTenantID(context.Background(), "tenant-1"),
		),
		string(authz.RoleViewer),
	)

	if tenantID, err := Authorize(viewerCtx, ActionObjectDownload); err != nil || tenantID != "tenant-1" {
		t.Fatalf("Authorize(read) = (%q, %v), want tenant-1 nil", tenantID, err)
	}
	if _, err := Authorize(viewerCtx, ActionObjectDelete); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("Authorize(write) error = %v, want permission denied", err)
	}
	if _, err := Authorize(viewerCtx, Action("storage.unknown")); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("Authorize(unknown) error = %v, want permission denied", err)
	}
}

func TestAuthorizeAcceptsVerifiedTenantAPIKey(t *testing.T) {
	ctx := contextkeys.WithAuthenticatedAPIKey(context.Background(), "tenant-1", "verified-key-hash")

	if tenantID, err := Authorize(ctx, ActionConnectionConfigure); err != nil || tenantID != "tenant-1" {
		t.Fatalf("Authorize() = (%q, %v), want tenant-1 nil", tenantID, err)
	}
}

func TestAuthorizeTenantRejectsCrossTenantAccess(t *testing.T) {
	ctx := contextkeys.WithAuthenticatedAPIKey(context.Background(), "tenant-1", "verified-key-hash")

	if _, err := AuthorizeTenant(ctx, ActionObjectList, "tenant-2"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("AuthorizeTenant() error = %v, want permission denied", err)
	}
	if _, err := AuthorizeTenant(ctx, ActionObjectList, ""); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("AuthorizeTenant(empty) error = %v, want permission denied", err)
	}
	if tenantID, err := AuthorizeTenant(ctx, ActionObjectList, "tenant-1"); err != nil || tenantID != "tenant-1" {
		t.Fatalf("AuthorizeTenant(match) = (%q, %v), want tenant-1 nil", tenantID, err)
	}
}

func TestSystemPrincipalIsExplicitAndTenantScoped(t *testing.T) {
	ctx := WithSystemPrincipal(context.Background(), "tenant-1")

	if tenantID, err := Authorize(ctx, ActionAdminReconcile); err != nil || tenantID != "tenant-1" {
		t.Fatalf("Authorize(system) = (%q, %v), want tenant-1 nil", tenantID, err)
	}
	if _, err := AuthorizeTenant(ctx, ActionAdminReconcile, "tenant-2"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("AuthorizeTenant(cross-tenant system) error = %v, want permission denied", err)
	}
	if _, err := Authorize(WithSystemPrincipal(context.Background(), ""), ActionAdminReconcile); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("Authorize(system without tenant) error = %v, want unauthenticated", err)
	}
}
