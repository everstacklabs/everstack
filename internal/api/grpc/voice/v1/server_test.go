package voice

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// TestRequireOrgID_FailClosedWithoutContext locks the post-2026-05-06
// contract for the voice service: tenant/org id derives from the
// request context, never `req.Msg.OrgId`. Empty context ⇒
// PermissionDenied. Regressing this restores the body-trust pattern
// that let any caller create / read / update / delete profiles
// across orgs.
func TestRequireOrgID_FailClosedWithoutContext(t *testing.T) {
	id, err := requireOrgID(context.Background())
	if err == nil {
		t.Fatalf("expected error, got id=%q", id)
	}
	if id != "" {
		t.Fatalf("expected empty id on failure, got %q", id)
	}
	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Code() != connect.CodePermissionDenied {
		t.Fatalf("expected CodePermissionDenied, got %v", err)
	}
}

func TestRequireOrgID_PrefersContextTenant(t *testing.T) {
	ctx := contextkeys.WithTenantID(context.Background(), "ctx-org")
	id, err := requireOrgID(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "ctx-org" {
		t.Fatalf("got %q, want %q", id, "ctx-org")
	}
}
