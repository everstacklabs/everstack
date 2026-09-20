package tools

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/everstacklabs/everstack/internal/mcp"
)

type testFunctions struct{ calls int }

func (f *testFunctions) ResolveFunction(_ context.Context, o Owner, id, name string) (Grant, error) {
	if o.InstanceID != "i1" || id != "fn1" {
		return Grant{}, ErrDenied
	}
	return Grant{ResourceID: id, Name: name, Description: "approved function", Parameters: map[string]any{"type": "object"}}, nil
}

type testApprovals struct{ checks int }

func (a *testApprovals) Check(context.Context, Owner, Grant, Invocation) (Decision, error) {
	a.checks++
	if a.checks == 1 {
		return Pending, nil
	}
	return Approved, nil
}
func (f *testFunctions) ExecuteFunction(_ context.Context, _ Owner, _ Grant, args map[string]any) (any, error) {
	f.calls++
	return map[string]any{"value": args["value"]}, nil
}

type testStore struct {
	mu           sync.Mutex
	grants       map[string]Grant
	results      map[string]Result
	active       map[string]bool
	failComplete bool
}

func (s *testStore) BindSession(_ context.Context, o Owner, sid string, gs []Grant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.grants == nil {
		s.grants = map[string]Grant{}
	}
	for _, g := range gs {
		s.grants[o.InstanceID+":"+sid+":"+g.UpstreamName] = g
	}
	return nil
}
func (s *testStore) GrantForCall(_ context.Context, o Owner, sid, name string) (Grant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.grants[o.InstanceID+":"+sid+":"+name]
	if !ok {
		return Grant{}, ErrDenied
	}
	return g, nil
}
func (s *testStore) BeginCall(_ context.Context, o Owner, in Invocation) (*Result, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := o.InstanceID + ":" + in.SessionID + ":" + in.TurnID + ":" + in.CallID
	if r, ok := s.results[key]; ok {
		return &r, false, nil
	}
	if s.active[key] {
		return nil, false, nil
	}
	if s.active == nil {
		s.active = map[string]bool{}
	}
	s.active[key] = true
	return nil, true, nil
}
func (s *testStore) CompleteCall(_ context.Context, o Owner, in Invocation, r Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failComplete {
		s.failComplete = false
		return errors.New("write failed")
	}
	if s.results == nil {
		s.results = map[string]Result{}
	}
	key := o.InstanceID + ":" + in.SessionID + ":" + in.TurnID + ":" + in.CallID
	s.results[key] = r
	delete(s.active, key)
	return nil
}
func (s *testStore) MarkRecoveryRequired(_ context.Context, o Owner, in Invocation, r Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.results == nil {
		s.results = map[string]Result{}
	}
	key := o.InstanceID + ":" + in.SessionID + ":" + in.TurnID + ":" + in.CallID
	s.results[key] = r
	delete(s.active, key)
	return nil
}
func (s *testStore) ReleaseCall(_ context.Context, o Owner, in Invocation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, o.InstanceID+":"+in.SessionID+":"+in.TurnID+":"+in.CallID)
	return nil
}

func TestFunctionResolutionInvocationAndDuplicateDelivery(t *testing.T) {
	ctx := context.Background()
	o := Owner{OrganizationID: "o", InstanceID: "i1", PrincipalID: "u"}
	store := &testStore{}
	functions := &testFunctions{}
	m, err := NewManager(store, functions, nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	set, err := m.ResolveTools(ctx, o, []Spec{{Type: "function", ResourceID: "fn1", Name: "lookup"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Configs) != 1 || !json.Valid(set.Configs[0]) {
		t.Fatalf("configs=%s", set.Configs)
	}
	if err = m.BindSession(ctx, o, "s1", set); err != nil {
		t.Fatal(err)
	}
	in := Invocation{Owner: o, SessionID: "s1", TurnID: "t1", CallID: "c1", Type: "function_call", Name: set.grants[0].UpstreamName, Arguments: json.RawMessage(`{"value":7}`)}
	first := m.HandleRequiredAction(ctx, in)
	second := m.HandleRequiredAction(ctx, in)
	if !first.Success || second.Output != first.Output {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
	if functions.calls != 1 {
		t.Fatalf("side effects=%d", functions.calls)
	}
}

func TestInvocationRechecksBoundOwnerAndRevocation(t *testing.T) {
	ctx := context.Background()
	o := Owner{OrganizationID: "o", InstanceID: "i1", PrincipalID: "u"}
	store := &testStore{}
	functions := &testFunctions{}
	m, _ := NewManager(store, functions, nil, nil, 0)
	set, _ := m.ResolveTools(ctx, o, []Spec{{Type: "function", ResourceID: "fn1", Name: "lookup"}})
	_ = m.BindSession(ctx, o, "s1", set)
	foreign := o
	foreign.InstanceID = "i2"
	r := m.HandleRequiredAction(ctx, Invocation{Owner: foreign, SessionID: "s1", TurnID: "t", CallID: "c", Type: "function_call", Name: set.grants[0].UpstreamName, Arguments: json.RawMessage(`{}`)})
	if r.Success || functions.calls != 0 {
		t.Fatalf("result=%#v calls=%d", r, functions.calls)
	}
	delete(store.grants, "i1:s1:"+set.grants[0].UpstreamName)
	r = m.HandleRequiredAction(ctx, Invocation{Owner: o, SessionID: "s1", TurnID: "t", CallID: "c", Type: "function_call", Name: set.grants[0].UpstreamName, Arguments: json.RawMessage(`{}`)})
	if r.Success {
		t.Fatal("revoked grant executed")
	}
}

func TestPendingApprovalReleasesClaimForApprovedRetry(t *testing.T) {
	ctx := context.Background()
	o := Owner{OrganizationID: "o", InstanceID: "i1", PrincipalID: "u"}
	store := &testStore{}
	functions := &testFunctions{}
	approvals := &testApprovals{}
	m, _ := NewManager(store, functions, nil, approvals, 0)
	set, _ := m.ResolveTools(ctx, o, []Spec{{Type: "function", ResourceID: "fn1", Name: "lookup"}})
	set.grants[0].RequiresApproval = true
	_ = m.BindSession(ctx, o, "s1", set)
	in := Invocation{Owner: o, SessionID: "s1", TurnID: "t1", CallID: "c1", Type: "function_call", Name: set.grants[0].UpstreamName, Arguments: json.RawMessage(`{}`)}
	first := m.HandleRequiredAction(ctx, in)
	second := m.HandleRequiredAction(ctx, in)
	if first.State != "pending" || !second.Success || functions.calls != 1 {
		t.Fatalf("first=%#v second=%#v calls=%d", first, second, functions.calls)
	}
}

func TestCompletedSideEffectIsNotRepeatedAfterResultPersistenceFailure(t *testing.T) {
	ctx := context.Background()
	o := Owner{OrganizationID: "o", InstanceID: "i1", PrincipalID: "u"}
	store := &testStore{failComplete: true}
	functions := &testFunctions{}
	m, _ := NewManager(store, functions, nil, nil, 0)
	set, _ := m.ResolveTools(ctx, o, []Spec{{Type: "function", ResourceID: "fn1", Name: "lookup"}})
	_ = m.BindSession(ctx, o, "s1", set)
	in := Invocation{Owner: o, SessionID: "s1", TurnID: "t1", CallID: "c1", Type: "function_call", Name: set.grants[0].UpstreamName, Arguments: json.RawMessage(`{}`)}
	first := m.HandleRequiredAction(ctx, in)
	second := m.HandleRequiredAction(ctx, in)
	if first.State != "recovery_required" || second.State != "recovery_required" || functions.calls != 1 {
		t.Fatalf("first=%#v second=%#v calls=%d", first, second, functions.calls)
	}
}

func TestMCPResolutionFailsClosedForForeignTenant(t *testing.T) {
	registry := mcp.NewRegistry(slog.Default())
	m, _ := NewManager(&testStore{}, nil, registry, nil, 0)
	_, err := m.ResolveTools(context.Background(), Owner{OrganizationID: "o", InstanceID: "i1", PrincipalID: "u"}, []Spec{{Type: "mcp", ResourceID: "foreign", Name: "tool"}})
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("error=%v", err)
	}
}
