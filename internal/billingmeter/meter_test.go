package billingmeter

import (
	"context"
	"testing"
	"time"
)

// DebitInference must guard its inputs before touching the database, so the
// no-op and validation paths are exercisable without a live connection.
func TestDebitInferenceGuards(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

	// Zero/negative cost is a no-op even with a nil db (nothing to record).
	if err := DebitInference(ctx, nil, "org-1", "req-1", MetricChat, 0, now); err != nil {
		t.Fatalf("zero cost should be a no-op, got %v", err)
	}
	if err := DebitInference(ctx, nil, "org-1", "req-1", MetricChat, -5, now); err != nil {
		t.Fatalf("negative cost should be a no-op, got %v", err)
	}

	// Missing org or request id is an error regardless of db.
	if err := DebitInference(ctx, nil, "", "req-1", MetricChat, 100, now); err == nil {
		t.Fatal("empty org should error")
	}
	if err := DebitInference(ctx, nil, "org-1", "", MetricChat, 100, now); err == nil {
		t.Fatal("empty request id should error")
	}

	// A real cost with a nil db is an error (would otherwise silently drop spend).
	if err := DebitInference(ctx, nil, "org-1", "req-1", MetricChat, 100, now); err == nil {
		t.Fatal("nil db with real cost should error")
	}
}

func TestResolveOrgGuards(t *testing.T) {
	// Nil db or empty tenant resolves to "no org" without error (unbilled).
	if org, ok, err := ResolveOrg(context.Background(), nil, ""); err != nil || ok || org != "" {
		t.Fatalf("empty tenant = (%q,%v,%v); want ('',false,nil)", org, ok, err)
	}
}
