package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

func TestRequireSandboxBillingHTTPRejectsUnpaidTenant(t *testing.T) {
	t.Parallel()

	manager := &sandbox.SandboxManager{}
	manager.SetSandboxBillingResolver(func(string) bool { return false })
	server := &Server{sandboxMgr: manager}
	ctx := contextkeys.WithTenantID(context.Background(), "tenant-free")
	req := httptest.NewRequest(http.MethodPost, "/v1/sandbox/instances/sbx/start", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()

	if server.requireSandboxBillingHTTP(recorder, req) {
		t.Fatal("unpaid tenant passed sandbox billing preflight")
	}
	if recorder.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusPaymentRequired)
	}
}

func TestRequireSandboxBillingHTTPAllowsPaidTenant(t *testing.T) {
	t.Parallel()

	manager := &sandbox.SandboxManager{}
	manager.SetSandboxBillingResolver(func(tenantID string) bool { return tenantID == "tenant-paid" })
	server := &Server{sandboxMgr: manager}
	ctx := contextkeys.WithTenantID(context.Background(), "tenant-paid")
	req := httptest.NewRequest(http.MethodPost, "/v1/sandbox/instances/sbx/start", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()

	if !server.requireSandboxBillingHTTP(recorder, req) {
		t.Fatalf("paid tenant rejected with status %d", recorder.Code)
	}
}
