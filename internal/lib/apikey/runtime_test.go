package apikey

import (
	"context"
	"testing"

	"github.com/everstacklabs/everstack/internal/lib/authratelimit"
)

type keylessCounter struct{ calls int }

func (s *keylessCounter) Consume(context.Context, string, authratelimit.Rule) (authratelimit.Decision, error) {
	s.calls++
	return authratelimit.Decision{}, nil
}
func (*keylessCounter) Peek(context.Context, string) (authratelimit.Window, error) {
	return authratelimit.Window{}, nil
}
func TestAdmissionNeverCreatesKeylessBuckets(t *testing.T) {
	t.Setenv("EVS_OTLP_RATE_LIMIT_RPM", "1")
	store := &keylessCounter{}
	old := SetRuntime(NewRuntime(store, nil))
	defer SetRuntime(old)
	for range 10 {
		ctx := Accept(context.Background(), "", "local-tenant", nil, SurfaceTelemetry, nil)
		if RetryAfter(ctx) != 0 {
			t.Fatal("anonymous caller was limited")
		}
	}
	if store.calls != 0 {
		t.Fatalf("anonymous traffic spent %d counters", store.calls)
	}
}
