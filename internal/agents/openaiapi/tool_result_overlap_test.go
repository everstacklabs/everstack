package openaiapi

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

type overlapApprovals struct{ review Approval }

func (a *overlapApprovals) ListPending(context.Context, Ownership) ([]Approval, error) {
	return nil, nil
}
func (a *overlapApprovals) Review(context.Context, Ownership, string) (Approval, error) {
	return a.review, nil
}
func (a *overlapApprovals) Resolve(context.Context, Ownership, string, string) error { return nil }

type overlapTools struct {
	mu       sync.Mutex
	approved bool
	claimed  bool
}

func (t *overlapTools) ClaimToolResultDelivery(context.Context, Ownership, string, ToolResult, string, time.Duration) (bool, string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.claimed {
		return false, "", nil
	}
	t.claimed = true
	return true, "pending", nil
}
func (*overlapTools) CompleteToolResultDelivery(context.Context, Ownership, string, ToolResult, string) error {
	return nil
}
func (*overlapTools) MarkToolResultDeliveryUncertain(context.Context, Ownership, string, ToolResult, string) error {
	return nil
}
func (*overlapTools) ReleaseToolResultDelivery(context.Context, Ownership, string, ToolResult, string) error {
	return nil
}

func (*overlapTools) ResolveTools(context.Context, Ownership, []ToolSpec) (ResolvedTools, error) {
	return ResolvedTools{}, nil
}
func (*overlapTools) BindSession(context.Context, Ownership, string, ResolvedTools) error { return nil }
func (t *overlapTools) HandleRequiredActions(context.Context, Ownership, string, UpstreamEvent) ([]ToolResult, error) {
	t.mu.Lock()
	approved := t.approved
	t.mu.Unlock()
	if !approved {
		return []ToolResult{{TurnID: "turn", CallID: "call", State: "pending"}}, nil
	}
	return []ToolResult{{TurnID: "turn", CallID: "call", State: "completed", Success: true, Output: `"marker"`}}, nil
}

type overlapUpstream struct {
	ambiguousUpstream
	emitAgain chan struct{}
	submits   chan struct{}
	release   chan struct{}
}

func (u *overlapUpstream) StreamEvents(ctx context.Context, _ ConnectionSecret, _ string, emit func(UpstreamEvent) error) error {
	action := UpstreamEvent{ID: "required", Type: "agent.session.required_action", ActionPayload: json.RawMessage(`{"turn_id":"turn","call_id":"call","type":"function_call","name":"tool","arguments":{}}`)}
	if err := emit(action); err != nil {
		return err
	}
	select {
	case <-u.emitAgain:
		return emit(action)
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (u *overlapUpstream) SubmitToolResult(ctx context.Context, _ ConnectionSecret, _ string, _ ToolResult) error {
	u.submits <- struct{}{}
	select {
	case <-u.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestResolveApprovalAndSupervisorDoNotSubmitSameCompletedResultConcurrently(t *testing.T) {
	o := Ownership{OrganizationID: "org", InstanceID: "instance", PrincipalID: "user", Role: "admin"}
	store := &restartStore{owner: o, connection: Connection{ID: "connection", credentialRef: `encrypted:{"api_key":"app"}`}, session: &Session{ID: "session", ConnectionID: "connection", UpstreamSessionID: "upstream", Status: "requires_action"}}
	tools := &overlapTools{}
	approvals := &overlapApprovals{review: Approval{ID: "approval", SessionID: "session", TurnID: "turn", CallID: "call", ToolName: "tool", Arguments: json.RawMessage(`{}`)}}
	upstream := &overlapUpstream{emitAgain: make(chan struct{}), submits: make(chan struct{}, 2), release: make(chan struct{})}
	service, err := NewService(store, testCipher{}, upstream, nil, tools, Config{})
	if err != nil {
		t.Fatal(err)
	}
	service.SetApprovals(approvals)
	service.SetRecoveryAuthorizer(allowRecovery{})
	service.startSupervisor(o, "session", "upstream")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = service.Close(ctx)
	})

	tools.mu.Lock()
	tools.approved = true
	tools.mu.Unlock()
	resolved := make(chan error, 1)
	go func() { resolved <- service.ResolveApproval(context.Background(), o, "approval", "approved") }()
	select {
	case <-upstream.submits:
	case <-time.After(time.Second):
		t.Fatal("approval path did not begin result submission")
	}
	close(upstream.emitAgain)
	select {
	case <-upstream.submits:
		t.Fatal("duplicate result submission overlapped")
	case <-time.After(100 * time.Millisecond):
	}
	close(upstream.release)
	if err := <-resolved; err != nil {
		t.Fatal(err)
	}
}
