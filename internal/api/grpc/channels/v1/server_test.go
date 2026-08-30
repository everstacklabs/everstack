package channels

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// TestRequireTenantID_FailClosedWithoutContext locks the post-2026-05-06
// contract: every channels RPC must derive its tenant id from the
// request context, never from the request body. Empty context ⇒
// PermissionDenied. Regressing this re-opens the cross-tenant leak
// surface in this package (msg.TenantId was the source of truth before
// the rewrite).
func TestRequireTenantID_FailClosedWithoutContext(t *testing.T) {
	tid, err := requireTenantID(context.Background())
	if err == nil {
		t.Fatalf("expected error, got tid=%q", tid)
	}
	if tid != "" {
		t.Fatalf("expected empty tenant id on failure, got %q", tid)
	}
	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Code() != connect.CodePermissionDenied {
		t.Fatalf("expected CodePermissionDenied, got %v", err)
	}
}

// TestRequireTenantID_PrefersContextTenant pins the trust anchor:
// whatever is in `contextkeys.GetTenantID(ctx)` is what every channels
// handler operates against. There is no body-level fallback.
func TestRequireTenantID_PrefersContextTenant(t *testing.T) {
	ctx := contextkeys.WithTenantID(context.Background(), "ctx-tenant")
	tid, err := requireTenantID(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tid != "ctx-tenant" {
		t.Fatalf("got %q, want %q", tid, "ctx-tenant")
	}
}
