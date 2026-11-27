package runtime

import (
	"context"
	"sync/atomic"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// A pause lands while an agent run is in flight: the loop stops at the next
// iteration boundary with a clear reason instead of spending more model
// calls.
func TestLoopStopsAtTheNextIterationWhenTheInstanceIsPaused(t *testing.T) {
	var checks atomic.Int32
	SetTenantActiveFn(func(_ context.Context, tenantID string) bool {
		checks.Add(1)
		return tenantID != "paused-instance"
	})
	t.Cleanup(func() { SetTenantActiveFn(nil) })

	provider := &modelCapturingProvider{}
	registry := gw.NewRegistry()
	registry.Register(provider)
	engine := NewEngine(registry, gw.NewRouter(registry, nil), nil)
	loop := NewLoop(engine, nil, NewEmitter(), LoopConfig{MaxIterations: 3})

	state, err := loop.Run(context.Background(), &LoopState{TurnNumber: 1}, &LoopInput{
		SessionID: "session-1",
		TenantID:  "paused-instance",
		Model:     "@openai/gpt-5.5",
		UserInput: "hello",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if state.FinishReason != FinishReasonInstancePaused || !state.Done {
		t.Fatalf("finish = %q done=%v, want %q", state.FinishReason, state.Done, FinishReasonInstancePaused)
	}
	if provider.request.Model != "" {
		t.Fatal("no model call may be made for a paused instance")
	}

	// An active tenant is unaffected.
	state, err = loop.Run(context.Background(), &LoopState{TurnNumber: 1}, &LoopInput{
		SessionID: "session-2",
		TenantID:  "active-instance",
		Model:     "@openai/gpt-5.5",
		UserInput: "hello",
	})
	if err != nil || state.FinishReason == FinishReasonInstancePaused || provider.request.Model != "gpt-5.5" {
		t.Fatalf("active run: finish=%q err=%v", state.FinishReason, err)
	}
	if checks.Load() < 2 {
		t.Fatal("the status must be checked at each iteration boundary")
	}
}
