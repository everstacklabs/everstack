package v1

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// TestResolveTenantID_FailClosedWithoutContext locks the post-2026-05-06
// contract for the MCP service: tenant id must come from context, never
// from the request body. Empty context ⇒ PermissionDenied. The
// requestTenantID parameter intentionally has no effect.
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
	if !errors.As(err, &ce) || ce.Code() != connect.CodePermissionDenied {
		t.Fatalf("expected CodePermissionDenied, got %v", err)
	}
}

// TestResolveTenantID_PrefersContextTenant pins the trust anchor: the
// tenant id flowing into every SQL filter (ListMcpServers,
// ListFederatedTools, loadServerFromDB, the OAuth init lookup) is the
// one this helper returns from context — and never the body argument.
func TestResolveTenantID_PrefersContextTenant(t *testing.T) {
	s := &Server{}
	ctx := contextkeys.WithTenantID(context.Background(), "ctx-tenant")
	tid, err := s.resolveTenantID(ctx, "body-tenant-must-be-ignored")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tid != "ctx-tenant" {
		t.Fatalf("got %q, want %q", tid, "ctx-tenant")
	}
}
