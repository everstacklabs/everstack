package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/cqrs"
	apikeylib "github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/query"
	"github.com/everstacklabs/everstack/pkg/tenant"
)

func TestWithAPIKeyValidation_BypassesForTenantContext(t *testing.T) {
	interceptor := NewAPIKeyInterceptor(false)
	handler := interceptor.WithAPIKeyValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/some-endpoint", nil)
	req = req.WithContext(tenant.WithConfig(req.Context(), &tenant.Config{
		InstanceID:     "inst-test",
		OrganizationID: "org-test",
		SchemaName:     "inst_test_schema",
	}))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected %d, got %d", http.StatusNoContent, rr.Code)
	}
}

func TestWithAPIKeyValidation_BypassesForTenantAuthenticatedFlag(t *testing.T) {
	interceptor := NewAPIKeyInterceptor(false)
	handler := interceptor.WithAPIKeyValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/some-endpoint", nil)
	req = req.WithContext(contextkeys.WithTenantAuthenticated(req.Context()))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected %d, got %d", http.StatusNoContent, rr.Code)
	}
}

func TestApplySessionAuthInstallsVerifiedRole(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/storage", nil)
	req = applySessionAuth(req, sessionAuth{
		valid:    true,
		userID:   "user-1",
		tenantID: "tenant-1",
		role:     "member",
	})

	if !contextkeys.IsTenantAuthenticated(req.Context()) {
		t.Fatal("session context is not marked authenticated")
	}
	if got := contextkeys.GetUserRole(req.Context()); got != "member" {
		t.Fatalf("role = %q, want member", got)
	}
}

func TestAPIKeyCacheHitInstallsVerifiedTenantPrincipal(t *testing.T) {
	interceptor := NewAPIKeyInterceptor(false)
	const key = "storage-api-key"
	hash, ok := apikeylib.HashWithSecret(key, "test-hash-secret")
	if !ok {
		t.Fatal("failed to hash test API key")
	}
	interceptor.cache[hash] = apiKeyCacheEntry{
		valid:     true,
		orgID:     "tenant-1",
		expiresAt: time.Now().Add(time.Minute),
	}

	handler := interceptor.WithAPIKeyValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !contextkeys.IsTenantAuthenticated(r.Context()) {
			t.Error("validated API key context is not marked authenticated")
		}
		if got := contextkeys.GetTenantID(r.Context()); got != "tenant-1" {
			t.Errorf("tenant = %q, want tenant-1", got)
		}
		if got := contextkeys.GetAPIKeyHash(r.Context()); got != hash {
			t.Errorf("API key hash = %q, want %q", got, hash)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/storage/upload", nil)
	req.Header.Set(common.EverstackApiKey, key)
	ctx := contextkeys.WithAPIKeyHashSecret(req.Context(), "test-hash-secret")
	ctx = cqrs.WithSystem(ctx, &cqrs.System{QueryBus: query.NewQueryBus()})
	req = req.WithContext(ctx)
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusNoContent)
	}
}
