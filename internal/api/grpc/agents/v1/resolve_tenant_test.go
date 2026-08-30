package v1

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// TestResolveTenantID_FailClosedWithoutContext pins the post-P0
// contract: when the request context has no tenant id and there is no
// single-tenant DB fallback available, the handler must return
// PermissionDenied. Regressing this re-opens the cross-tenant leak
// closed on 2026-05-06 — see project_tenant_isolation_bugs.md.
//
// We construct the Server with db=nil so singleTenantFallback is
// short-circuited; this isolates the auth-context branch from any
// "lone organizations row" environment quirks in the test DB.
func TestResolveTenantID_FailClosedWithoutContext(t *testing.T) {
	s := &Server{}
	tid, err := s.resolveTenantID(context.Background(), "client-supplied-must-be-ignored")
	if err == nil {
		t.Fatalf("expected error when context has no tenant id, got tid=%q", tid)
	}
	if tid != "" {
		t.Fatalf("expected empty tenant id on failure, got %q", tid)
	}
	var ce *connect.Error
	if !errors.As(err, &ce) {
		t.Fatalf("expected *connect.Error, got %T: %v", err, err)
	}
	if ce.Code() != connect.CodePermissionDenied {
		t.Fatalf("expected CodePermissionDenied, got %v", ce.Code())
	}
}

// TestResolveTenantID_PrefersContextTenant locks the source-of-truth
// contract: when context has a tenant id, that's what's used. The
// `requestTenantID` parameter remains on the signature for caller
// compatibility but must never be consulted (tested by passing a
// distinct value and asserting the context value wins).
func TestResolveTenantID_PrefersContextTenant(t *testing.T) {
	s := &Server{}
	ctx := contextkeys.WithTenantID(context.Background(), "ctx-tenant")
	tid, err := s.resolveTenantID(ctx, "body-tenant-must-be-ignored")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tid != "ctx-tenant" {
		t.Fatalf("expected context tenant id, got %q", tid)
	}
}
