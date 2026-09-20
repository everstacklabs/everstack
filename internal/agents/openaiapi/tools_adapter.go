package openaiapi

import (
	"context"
	"encoding/json"
	"time"

	toolpkg "github.com/everstacklabs/everstack/internal/agents/openaiapi/tools"
)

type ToolsAdapter struct {
	Manager   *toolpkg.Manager
	Approvals *toolpkg.PostgresApprovalGate
}

func ownerTools(o Ownership) toolpkg.Owner {
	return toolpkg.Owner{OrganizationID: o.OrganizationID, InstanceID: o.InstanceID, PrincipalID: o.PrincipalID, Role: o.Role}
}
func (a ToolsAdapter) ListPending(ctx context.Context, o Ownership) ([]Approval, error) {
	rows, err := a.Approvals.ListPending(ctx, ownerTools(o))
	if err != nil {
		return nil, err
	}
	out := make([]Approval, 0, len(rows))
	for _, r := range rows {
		out = append(out, Approval{ID: r.ID, SessionID: r.SessionID, TurnID: r.TurnID, CallID: r.CallID, ToolName: r.ToolName, ResourceKind: r.ResourceKind, ResourceID: r.ResourceID, Status: r.Status, RequestedBy: r.RequestedBy, Arguments: sanitizeJSON(r.Arguments), RequestedAt: r.RequestedAt, ExpiresAt: r.ExpiresAt})
	}
	return out, nil
}
func (a ToolsAdapter) Review(ctx context.Context, o Ownership, id string) (Approval, error) {
	r, e := a.Approvals.Review(ctx, ownerTools(o), id)
	if e != nil {
		return Approval{}, ErrNotFound
	}
	return Approval{ID: r.ID, SessionID: r.SessionID, TurnID: r.TurnID, CallID: r.CallID, ToolName: r.ToolName, ResourceKind: r.ResourceKind, ResourceID: r.ResourceID, Status: r.Status, RequestedBy: r.RequestedBy, Arguments: r.Arguments, RequestedAt: r.RequestedAt, ExpiresAt: r.ExpiresAt}, nil
}
func (a ToolsAdapter) Resolve(ctx context.Context, o Ownership, id, decision string) error {
	return a.Approvals.Resolve(ctx, ownerTools(o), id, toolpkg.Decision(decision))
}
func (a ToolsAdapter) ResolveTools(ctx context.Context, o Ownership, specs []ToolSpec) (ResolvedTools, error) {
	in := make([]toolpkg.Spec, len(specs))
	for i, s := range specs {
		in[i] = toolpkg.Spec{Type: s.Type, ResourceID: s.ResourceID, Name: s.Name}
	}
	set, err := a.Manager.ResolveTools(ctx, ownerTools(o), in)
	return ResolvedTools{Configs: set.Configs, binding: set}, err
}
func (a ToolsAdapter) BindSession(ctx context.Context, o Ownership, id string, set ResolvedTools) error {
	resolved, ok := set.binding.(toolpkg.ResolvedSet)
	if !ok {
		return ErrInvalid
	}
	return a.Manager.BindSession(ctx, ownerTools(o), id, resolved)
}
func (a ToolsAdapter) HandleRequiredActions(ctx context.Context, o Ownership, id string, e UpstreamEvent) ([]ToolResult, error) {
	type action struct {
		TurnID    string          `json:"turn_id"`
		CallID    string          `json:"call_id"`
		Type      string          `json:"type"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	var payload struct {
		action
		Session struct {
			RequiredActions []action `json:"required_actions"`
		} `json:"session"`
	}
	actionPayload := e.ActionPayload
	if len(actionPayload) == 0 {
		actionPayload = e.Payload
	}
	if json.Unmarshal(actionPayload, &payload) != nil {
		return nil, nil
	}
	actions := payload.Session.RequiredActions
	if payload.action.CallID != "" {
		actions = append([]action{payload.action}, actions...)
	}
	results := make([]ToolResult, 0, len(actions))
	for _, selected := range actions {
		if selected.CallID == "" {
			continue
		}
		r := a.Manager.HandleRequiredAction(ctx, toolpkg.Invocation{Owner: ownerTools(o), SessionID: id, TurnID: selected.TurnID, CallID: selected.CallID, Type: selected.Type, Name: selected.Name, Arguments: selected.Arguments})
		results = append(results, ToolResult{TurnID: r.TurnID, CallID: r.CallID, Success: r.Success, Output: r.Output, Error: r.Error, State: r.State})
	}
	return results, nil
}

func (a ToolsAdapter) ClaimToolResultDelivery(ctx context.Context, o Ownership, sessionID string, result ToolResult, token string, lease time.Duration) (bool, string, error) {
	return a.Manager.ClaimDelivery(ctx, ownerTools(o), toolpkg.Invocation{SessionID: sessionID, TurnID: result.TurnID, CallID: result.CallID}, token, lease)
}
func (a ToolsAdapter) ReleaseToolResultDelivery(ctx context.Context, o Ownership, sessionID string, result ToolResult, token string) error {
	return a.Manager.ReleaseDelivery(ctx, ownerTools(o), toolpkg.Invocation{SessionID: sessionID, TurnID: result.TurnID, CallID: result.CallID}, token)
}
func (a ToolsAdapter) CompleteToolResultDelivery(ctx context.Context, o Ownership, sessionID string, result ToolResult, token string) error {
	return a.Manager.CompleteDelivery(ctx, ownerTools(o), toolpkg.Invocation{SessionID: sessionID, TurnID: result.TurnID, CallID: result.CallID}, token)
}
func (a ToolsAdapter) MarkToolResultDeliveryUncertain(ctx context.Context, o Ownership, sessionID string, result ToolResult, token string) error {
	return a.Manager.MarkDeliveryUncertain(ctx, ownerTools(o), toolpkg.Invocation{SessionID: sessionID, TurnID: result.TurnID, CallID: result.CallID}, token)
}
