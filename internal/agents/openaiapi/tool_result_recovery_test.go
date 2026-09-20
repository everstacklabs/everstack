package openaiapi

import (
	"context"
	"sync"
	"testing"
	"time"
)

type recoveryDeliveryTools struct {
	mu        sync.Mutex
	claimed   bool
	recovery  string
	marks     int
	completes int
	releases  int
}

func (*recoveryDeliveryTools) ResolveTools(context.Context, Ownership, []ToolSpec) (ResolvedTools, error) {
	return ResolvedTools{}, nil
}
func (*recoveryDeliveryTools) BindSession(context.Context, Ownership, string, ResolvedTools) error {
	return nil
}
func (*recoveryDeliveryTools) HandleRequiredActions(context.Context, Ownership, string, UpstreamEvent) ([]ToolResult, error) {
	return nil, nil
}
func (t *recoveryDeliveryTools) ClaimToolResultDelivery(context.Context, Ownership, string, ToolResult, string, time.Duration) (bool, string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.claimed, t.recovery, nil
}
func (t *recoveryDeliveryTools) CompleteToolResultDelivery(context.Context, Ownership, string, ToolResult, string) error {
	t.mu.Lock()
	t.completes++
	t.mu.Unlock()
	return nil
}
func (t *recoveryDeliveryTools) MarkToolResultDeliveryUncertain(context.Context, Ownership, string, ToolResult, string) error {
	t.mu.Lock()
	t.marks++
	t.mu.Unlock()
	return nil
}
func (t *recoveryDeliveryTools) ReleaseToolResultDelivery(context.Context, Ownership, string, ToolResult, string) error {
	t.mu.Lock()
	t.releases++
	t.mu.Unlock()
	return nil
}

type recoveryDeliveryUpstream struct {
	ambiguousUpstream
	mu             sync.Mutex
	accepted       bool
	acceptanceGets int
	submits        int
}

func (u *recoveryDeliveryUpstream) ToolResultAccepted(context.Context, ConnectionSecret, string, ToolResult) (bool, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.acceptanceGets++
	return u.accepted, nil
}
func (u *recoveryDeliveryUpstream) SubmitToolResult(context.Context, ConnectionSecret, string, ToolResult) error {
	u.mu.Lock()
	u.submits++
	u.mu.Unlock()
	return nil
}

func newToolResultRecoveryService(t *testing.T, tools *recoveryDeliveryTools, upstream *recoveryDeliveryUpstream) (*Service, *restartStore, Ownership) {
	t.Helper()
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	store := &restartStore{owner: o}
	service, err := NewService(store, testCipher{}, upstream, nil, tools, Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return service, store, o
}

func TestRecoveredToolResultWithoutExactSavedMatchIsNeverResubmitted(t *testing.T) {
	for _, recovery := range []string{"expired", "uncertain"} {
		t.Run(recovery, func(t *testing.T) {
			tools := &recoveryDeliveryTools{claimed: true, recovery: recovery}
			upstream := &recoveryDeliveryUpstream{}
			service, store, owner := newToolResultRecoveryService(t, tools, upstream)
			result := ToolResult{TurnID: "turn", CallID: "call", State: "completed", Success: true, Output: `"marker"`}

			submitted, err := service.submitToolResult(context.Background(), owner, "session", ConnectionSecret{APIKey: "test"}, "upstream", result)
			if err == nil || submitted {
				t.Fatalf("submitted=%v err=%v, want uncertain reconciliation error", submitted, err)
			}
			tools.mu.Lock()
			marks, completes, releases := tools.marks, tools.completes, tools.releases
			tools.mu.Unlock()
			upstream.mu.Lock()
			acceptanceGets, submits := upstream.acceptanceGets, upstream.submits
			upstream.mu.Unlock()
			if acceptanceGets != 1 || submits != 0 || marks != 1 || completes != 0 || releases != 0 || len(store.events) != 0 {
				t.Fatalf("acceptance=%d submits=%d marks=%d completes=%d releases=%d events=%d", acceptanceGets, submits, marks, completes, releases, len(store.events))
			}
		})
	}
}

func TestRecoveredToolResultExactSavedMatchAcknowledgesWithoutPost(t *testing.T) {
	tools := &recoveryDeliveryTools{claimed: true, recovery: "expired"}
	upstream := &recoveryDeliveryUpstream{accepted: true}
	service, store, owner := newToolResultRecoveryService(t, tools, upstream)
	result := ToolResult{TurnID: "turn", CallID: "call", State: "completed", Success: false, Error: "failed"}

	submitted, err := service.submitToolResult(context.Background(), owner, "session", ConnectionSecret{APIKey: "test"}, "upstream", result)
	if err != nil || !submitted {
		t.Fatalf("submitted=%v err=%v", submitted, err)
	}
	tools.mu.Lock()
	marks, completes := tools.marks, tools.completes
	tools.mu.Unlock()
	upstream.mu.Lock()
	acceptanceGets, submits := upstream.acceptanceGets, upstream.submits
	upstream.mu.Unlock()
	if acceptanceGets != 1 || submits != 0 || marks != 0 || completes != 1 || len(store.events) != 1 || !store.events[0].ToolFailed {
		t.Fatalf("acceptance=%d submits=%d marks=%d completes=%d events=%+v", acceptanceGets, submits, marks, completes, store.events)
	}
}

func TestSubmittedToolResultRetryRepairsMissingLocalEventWithoutPost(t *testing.T) {
	tools := &recoveryDeliveryTools{claimed: false, recovery: "submitted"}
	upstream := &recoveryDeliveryUpstream{}
	service, store, owner := newToolResultRecoveryService(t, tools, upstream)
	result := ToolResult{TurnID: "turn", CallID: "call", State: "completed", Success: true, Output: `"marker"`}

	submitted, err := service.submitToolResult(context.Background(), owner, "session", ConnectionSecret{APIKey: "test"}, "upstream", result)
	if err != nil || !submitted {
		t.Fatalf("submitted=%v err=%v", submitted, err)
	}
	upstream.mu.Lock()
	acceptanceGets, submits := upstream.acceptanceGets, upstream.submits
	upstream.mu.Unlock()
	if acceptanceGets != 0 || submits != 0 || len(store.events) != 1 || store.events[0].ToolFailed {
		t.Fatalf("acceptance=%d submits=%d events=%+v", acceptanceGets, submits, store.events)
	}
	// Repair is idempotent if both paths observe submitted state.
	if _, err = service.submitToolResult(context.Background(), owner, "session", ConnectionSecret{APIKey: "test"}, "upstream", result); err != nil {
		t.Fatal(err)
	}
	if len(store.events) != 1 {
		t.Fatalf("duplicate durable events=%d", len(store.events))
	}
}
