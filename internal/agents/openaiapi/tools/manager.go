// Package tools maps explicitly granted Everstack functions and MCP tools to
// OpenAI Agents function tools. Execution stays inside Everstack.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/mcp"
)

type Owner struct{ OrganizationID, InstanceID, PrincipalID, Role string }
type Spec struct{ Type, ResourceID, Name string }
type Grant struct {
	Kind, ResourceID, Name, UpstreamName, Description string
	Parameters                                        map[string]any
	RequiresApproval                                  bool
}
type ResolvedSet struct {
	Configs []json.RawMessage
	grants  []Grant
}
type Invocation struct {
	Owner                                 Owner
	SessionID, TurnID, CallID, Type, Name string
	Arguments                             json.RawMessage
}
type Result struct {
	TurnID, CallID string
	Success        bool
	Output         string
	Error          string
	State          string
}

var ErrDenied = errors.New("tool invocation denied")
var ErrPending = errors.New("tool invocation is awaiting approval or prior execution")

type FunctionResolver interface {
	ResolveFunction(context.Context, Owner, string, string) (Grant, error)
	ExecuteFunction(context.Context, Owner, Grant, map[string]any) (any, error)
}
type ApprovalGate interface {
	Check(context.Context, Owner, Grant, Invocation) (Decision, error)
}
type Decision string

const (
	Approved Decision = "approved"
	Pending  Decision = "pending"
	Denied   Decision = "denied"
)

// Store must atomically claim a call before execution. Implementations are
// durable and tenant/instance scoped; BeginCall returns a saved or in-progress
// result when acquired is false.
type Store interface {
	BindSession(context.Context, Owner, string, []Grant) error
	GrantForCall(context.Context, Owner, string, string) (Grant, error)
	BeginCall(context.Context, Owner, Invocation) (existing *Result, acquired bool, err error)
	ReleaseCall(context.Context, Owner, Invocation) error
	CompleteCall(context.Context, Owner, Invocation, Result) error
	MarkRecoveryRequired(context.Context, Owner, Invocation, Result) error
}
type DeliveryStore interface {
	ClaimDelivery(context.Context, Owner, Invocation, string, time.Duration) (bool, string, error)
	CompleteDelivery(context.Context, Owner, Invocation, string) error
	MarkDeliveryUncertain(context.Context, Owner, Invocation, string) error
	ReleaseDelivery(context.Context, Owner, Invocation, string) error
}

type Manager struct {
	store     Store
	functions FunctionResolver
	mcp       *mcp.Registry
	approvals ApprovalGate
	timeout   time.Duration
}

func (m *Manager) ClaimDelivery(ctx context.Context, o Owner, in Invocation, token string, lease time.Duration) (bool, string, error) {
	store, ok := m.store.(DeliveryStore)
	if !ok {
		return false, "", errors.New("durable tool result delivery is unavailable")
	}
	return store.ClaimDelivery(ctx, o, in, token, lease)
}
func (m *Manager) ReleaseDelivery(ctx context.Context, o Owner, in Invocation, token string) error {
	store, ok := m.store.(DeliveryStore)
	if !ok {
		return errors.New("durable tool result delivery is unavailable")
	}
	return store.ReleaseDelivery(ctx, o, in, token)
}
func (m *Manager) CompleteDelivery(ctx context.Context, o Owner, in Invocation, token string) error {
	store, ok := m.store.(DeliveryStore)
	if !ok {
		return errors.New("durable tool result delivery is unavailable")
	}
	return store.CompleteDelivery(ctx, o, in, token)
}
func (m *Manager) MarkDeliveryUncertain(ctx context.Context, o Owner, in Invocation, token string) error {
	store, ok := m.store.(DeliveryStore)
	if !ok {
		return errors.New("durable tool result delivery is unavailable")
	}
	return store.MarkDeliveryUncertain(ctx, o, in, token)
}

func NewManager(store Store, functions FunctionResolver, mcpRegistry *mcp.Registry, approvals ApprovalGate, timeout time.Duration) (*Manager, error) {
	if store == nil {
		return nil, errors.New("durable tool store is required")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Manager{store: store, functions: functions, mcp: mcpRegistry, approvals: approvals, timeout: timeout}, nil
}

func (m *Manager) ResolveTools(ctx context.Context, o Owner, specs []Spec) (ResolvedSet, error) {
	if !validOwner(o) {
		return ResolvedSet{}, ErrDenied
	}
	seen := map[string]bool{}
	out := ResolvedSet{}
	for _, s := range specs {
		var g Grant
		var err error
		switch s.Type {
		case "function":
			if m.functions == nil {
				return ResolvedSet{}, fmt.Errorf("functions unavailable")
			}
			g, err = m.functions.ResolveFunction(ctx, o, s.ResourceID, s.Name)
			g.Kind = "function"
		case "mcp":
			g, err = m.resolveMCP(o, s)
		default:
			return ResolvedSet{}, fmt.Errorf("unsupported tool type %q", s.Type)
		}
		if err != nil {
			return ResolvedSet{}, err
		}
		if g.ResourceID == "" || g.Name == "" {
			return ResolvedSet{}, ErrDenied
		}
		if g.UpstreamName == "" {
			g.UpstreamName = safeName(g.Kind + "__" + g.ResourceID + "__" + g.Name)
		}
		if seen[g.UpstreamName] {
			return ResolvedSet{}, fmt.Errorf("duplicate upstream tool name %q", g.UpstreamName)
		}
		seen[g.UpstreamName] = true
		b, err := json.Marshal(map[string]any{"type": "function", "name": g.UpstreamName, "description": g.Description, "parameters": schema(g.Parameters)})
		if err != nil {
			return ResolvedSet{}, err
		}
		out.Configs = append(out.Configs, b)
		out.grants = append(out.grants, g)
	}
	return out, nil
}
func (m *Manager) BindSession(ctx context.Context, o Owner, sessionID string, set ResolvedSet) error {
	if !validOwner(o) || sessionID == "" {
		return ErrDenied
	}
	return m.store.BindSession(ctx, o, sessionID, append([]Grant(nil), set.grants...))
}

func (m *Manager) HandleRequiredAction(ctx context.Context, in Invocation) Result {
	r := Result{TurnID: in.TurnID, CallID: in.CallID, State: "failed"}
	if !validOwner(in.Owner) || in.SessionID == "" || in.TurnID == "" || in.CallID == "" || in.Type != "function_call" {
		r.Error = "invalid or unauthorized tool invocation"
		return r
	}
	g, err := m.store.GrantForCall(ctx, in.Owner, in.SessionID, in.Name)
	if err != nil {
		r.Error = "tool grant unavailable or revoked"
		return r
	}
	existing, acquired, err := m.store.BeginCall(ctx, in.Owner, in)
	if err != nil {
		r.Error = "could not reserve tool invocation"
		return r
	}
	if !acquired {
		if existing != nil {
			return *existing
		}
		r.State = "pending"
		r.Error = ErrPending.Error()
		return r
	}
	if g.RequiresApproval {
		if m.approvals == nil {
			r.Error = ErrDenied.Error()
			return m.complete(ctx, in, r)
		}
		decision, e := m.approvals.Check(ctx, in.Owner, g, in)
		if e != nil {
			r.Error = "approval check failed"
			return m.complete(ctx, in, r)
		}
		if decision == Pending {
			r.State = "pending"
			r.Error = ErrPending.Error()
			_ = m.store.ReleaseCall(ctx, in.Owner, in)
			return r
		}
		if decision != Approved {
			r.Error = ErrDenied.Error()
			return m.complete(ctx, in, r)
		}
	}
	var args map[string]any
	if err = json.Unmarshal(in.Arguments, &args); err != nil {
		r.Error = "function arguments must be a JSON object"
		return m.complete(ctx, in, r)
	}
	callCtx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	var value any
	if g.Kind == "mcp" {
		value, err = m.executeMCP(callCtx, in.Owner, g, args)
	} else if m.functions != nil {
		value, err = m.functions.ExecuteFunction(callCtx, in.Owner, g, args)
	} else {
		err = errors.New("functions unavailable")
	}
	if err != nil {
		r.Error = "tool execution failed"
		return m.complete(ctx, in, r)
	}
	b, err := json.Marshal(value)
	if err != nil {
		r.Error = "tool result could not be encoded"
	} else {
		r.Success = true
		r.State = "completed"
		r.Output = string(b)
	}
	return m.complete(ctx, in, r)
}

func (m *Manager) complete(ctx context.Context, in Invocation, r Result) Result {
	if err := m.store.CompleteCall(ctx, in.Owner, in, r); err != nil {
		uncertain := Result{TurnID: in.TurnID, CallID: in.CallID, State: "recovery_required", Error: "tool result persistence failed; automatic execution disabled"}
		_ = m.store.MarkRecoveryRequired(ctx, in.Owner, in, uncertain)
		return uncertain
	}
	return r
}

func validOwner(o Owner) bool {
	return strings.TrimSpace(o.OrganizationID) != "" && strings.TrimSpace(o.InstanceID) != "" && strings.TrimSpace(o.PrincipalID) != ""
}
func schema(v map[string]any) map[string]any {
	if v != nil {
		return v
	}
	return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

func safeName(v string) string {
	v = unsafeName.ReplaceAllString(v, "_")
	if len(v) > 64 {
		v = v[:64]
	}
	return v
}
