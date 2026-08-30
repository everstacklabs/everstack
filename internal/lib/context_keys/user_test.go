package contextkeys

import (
	"context"
	"testing"
)

func TestWithAuthenticatedAPIKeyInstallsVerifiedPrincipal(t *testing.T) {
	ctx := WithAuthenticatedAPIKey(context.Background(), "tenant-1", "hash-1")
	if !IsTenantAuthenticated(ctx) {
		t.Fatal("API key context is not marked authenticated")
	}
	if got := GetTenantID(ctx); got != "tenant-1" {
		t.Fatalf("tenant = %q, want tenant-1", got)
	}
	if got := GetAPIKeyHash(ctx); got != "hash-1" {
		t.Fatalf("API key hash = %q, want hash-1", got)
	}
	if !HasVerifiedTenantPrincipal(ctx) {
		t.Fatal("validated API key did not establish a verified tenant principal")
	}
}

func TestHasVerifiedTenantPrincipalRejectsUntrustedTenantContext(t *testing.T) {
	tenantOnly := WithTenantID(context.Background(), "tenant-1")
	if HasVerifiedTenantPrincipal(tenantOnly) {
		t.Fatal("tenant id without authentication evidence was trusted")
	}

	legacyHeaderPath := WithTenantAuthenticated(tenantOnly)
	if HasVerifiedTenantPrincipal(legacyHeaderPath) {
		t.Fatal("tenant id plus legacy authenticated marker was trusted without principal evidence")
	}
}

func TestHasVerifiedTenantPrincipalAcceptsValidatedSession(t *testing.T) {
	ctx := WithTenantID(context.Background(), "tenant-1")
	ctx = WithCloudUserID(ctx, "user-1")
	ctx = WithTenantAuthenticated(ctx)

	if !HasVerifiedTenantPrincipal(ctx) {
		t.Fatal("validated user session did not establish a verified tenant principal")
	}
}
