package openaiapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/agents/openaiapi/upstream"
)

// UpstreamAdapter constructs a credential-scoped upstream client for each
// operation. Application credentials therefore never become process-global or
// enter an executor environment. BaseURL is trusted server configuration.
type UpstreamAdapter struct {
	BaseURL string
	Timeout time.Duration
}

func (a UpstreamAdapter) ToolResultTimeout() time.Duration { return a.Timeout }

func (a UpstreamAdapter) client(s ConnectionSecret) (*upstream.Client, error) {
	return upstream.NewClient(upstream.Config{BaseURL: a.BaseURL, APIKey: s.APIKey, Project: s.ProjectID, Organization: s.OrganizationID, Timeout: a.Timeout})
}
func (a UpstreamAdapter) ValidateConnection(ctx context.Context, s ConnectionSecret) error {
	c, e := a.client(s)
	if e != nil {
		return e
	}
	_, e = c.ListSessions(ctx, upstream.ListOptions{Limit: 1})
	if e != nil {
		return wrappedUpstreamError{e}
	}
	return nil
}
func (a UpstreamAdapter) CreateSession(ctx context.Context, s ConnectionSecret, r CreateSessionRequest) (*UpstreamSession, error) {
	c, err := a.client(s)
	if err != nil {
		return nil, err
	}
	tools := make([]json.RawMessage, 0, len(r.Tools))
	if len(r.resolvedTools) > 0 {
		tools = append(tools, r.resolvedTools...)
	} else {
		for _, t := range r.Tools {
			b, _ := json.Marshal(t)
			tools = append(tools, b)
		}
	}
	env, _ := json.Marshal(r.Environment)
	var input any
	if len(r.Input) > 0 {
		if err := json.Unmarshal(r.Input, &input); err != nil {
			return nil, ErrInvalid
		}
	}
	u, err := c.CreateSession(ctx, upstream.CreateSessionRequest{Agent: upstream.Agent{Model: r.Model, Instructions: r.Instructions, Tools: tools}, Environment: env, Input: input})
	if err != nil {
		return nil, wrappedUpstreamError{err}
	}
	return fromUpstream(u), nil
}

// The upstream beta API has no lookup by Idempotency-Key. An ambiguous create
// remains recovery_pending rather than issuing a blind retry after restart.
func (a UpstreamAdapter) RecoverSession(context.Context, ConnectionSecret, string) (*UpstreamSession, error) {
	return nil, ErrNotFound
}
func (a UpstreamAdapter) GetSession(ctx context.Context, s ConnectionSecret, id string) (*UpstreamSession, error) {
	c, e := a.client(s)
	if e != nil {
		return nil, e
	}
	u, e := c.GetSession(ctx, id)
	if e != nil {
		return nil, wrappedUpstreamError{e}
	}
	return fromUpstream(u), nil
}
func (a UpstreamAdapter) SendInput(ctx context.Context, s ConnectionSecret, id string, r InputRequest) error {
	c, e := a.client(s)
	if e != nil {
		return e
	}
	var msgs []upstream.InputMessage
	var inputText string
	if json.Unmarshal(r.Input, &inputText) == nil {
		msgs = []upstream.InputMessage{{Role: "user", Content: []upstream.InputContent{{Type: "input_text", Text: inputText}}}}
	} else if json.Unmarshal(r.Input, &msgs) != nil {
		return ErrInvalid
	}
	if e = c.SendInputWithOptions(ctx, id, msgs, upstream.EventOptions{IdempotencyKey: r.IdempotencyKey}); e != nil {
		return wrappedUpstreamError{e}
	}
	return nil
}
func (a UpstreamAdapter) StreamEvents(ctx context.Context, s ConnectionSecret, id string, emit func(UpstreamEvent) error) error {
	c, e := a.client(s)
	if e != nil {
		return e
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Subscribe may wait indefinitely for the upstream to send response headers.
	// Establish and consume it off the reconciliation path so a required action
	// which already exists in the session snapshot cannot be missed.
	streamEvents := make(chan upstream.Event)
	streamErrors := make(chan error, 1)
	go consumeUpstreamStream(runCtx, c, id, streamEvents, streamErrors)

	var itemState itemReconcileState
	if e = reconcileUpstreamState(runCtx, c, id, &itemState, emit); e != nil {
		return e
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-streamEvents:
			if e = emit(streamEvent(event)); e != nil {
				return e
			}
		case streamErr := <-streamErrors:
			return wrappedUpstreamError{streamErr}
		case <-ticker.C:
			if e = reconcileUpstreamState(runCtx, c, id, &itemState, emit); e != nil {
				return e
			}
		}
	}
}

func consumeUpstreamStream(ctx context.Context, c *upstream.Client, id string, events chan<- upstream.Event, failures chan<- error) {
	stream, err := c.Subscribe(ctx, id, upstream.StreamOptions{})
	if err == nil {
		for {
			event, nextErr := stream.Next()
			if nextErr != nil {
				err = nextErr
				break
			}
			select {
			case events <- event:
			case <-ctx.Done():
				_ = stream.Close()
				return
			}
		}
		_ = stream.Close()
	}
	select {
	case failures <- err:
	case <-ctx.Done():
	}
}

type itemReconcileState struct {
	// refreshAfter is the cursor immediately before the most recent page. Items
	// can change from in-progress to completed without changing ID, so each poll
	// refetches this bounded tail instead of advancing past it forever.
	refreshAfter string
}

func reconcileUpstreamState(ctx context.Context, c *upstream.Client, id string, state *itemReconcileState, emit func(UpstreamEvent) error) error {
	session, err := c.GetSession(ctx, id)
	if err != nil {
		return wrappedUpstreamError{err}
	}
	turnID := currentTurnID(session)
	for _, action := range session.RequiredActions {
		if action.TurnID == "" {
			action.TurnID = turnID
		}
		payload, marshalErr := json.Marshal(struct {
			TurnID    string          `json:"turn_id,omitempty"`
			CallID    string          `json:"call_id,omitempty"`
			Type      string          `json:"type"`
			Name      string          `json:"name,omitempty"`
			Arguments json.RawMessage `json:"arguments,omitempty"`
		}{action.TurnID, action.CallID, action.Type, action.Name, action.Arguments})
		if marshalErr != nil {
			return marshalErr
		}
		eventID := "required-action:" + action.TurnID + ":" + action.CallID
		if err = emit(UpstreamEvent{ID: eventID, Type: "agent.session.required_action", Version: "agents=v1", ItemID: action.CallID, Payload: sanitizeJSON(payload), ActionPayload: payload}); err != nil {
			return err
		}
	}

	after := state.refreshAfter
	for pageNumber := 0; pageNumber < 1000; pageNumber++ {
		pageStart := after
		page, listErr := c.ListItems(ctx, id, upstream.ListOptions{After: after, Order: "asc", Limit: 100})
		if listErr != nil {
			return wrappedUpstreamError{listErr}
		}
		for _, item := range page.Data {
			payload := sanitizeJSON(item.Raw)
			if err = emit(UpstreamEvent{ID: "item:" + item.ID + ":" + payloadDigest(payload), Type: "agent.session.item.recovered", Version: "agents=v1", ItemID: item.ID, Payload: payload, Failed: strings.EqualFold(item.Status, "failed")}); err != nil {
				return err
			}
		}
		next := page.LastID
		if next == "" && len(page.Data) > 0 {
			next = page.Data[len(page.Data)-1].ID
		}
		if !page.HasMore {
			state.refreshAfter = pageStart
			return nil
		}
		if next == "" || next == after {
			return fmt.Errorf("openai agents list items: pagination did not advance")
		}
		after = next
	}
	return fmt.Errorf("openai agents list items: pagination limit exceeded")
}

func currentTurnID(session *upstream.Session) string {
	if session == nil || len(session.Raw) == 0 {
		return ""
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(session.Raw, &raw) != nil {
		return ""
	}
	for _, key := range []string{"current_turn_id", "turn_id", "current_turn"} {
		var id string
		if json.Unmarshal(raw[key], &id) == nil && id != "" {
			return id
		}
	}
	for _, key := range []string{"current_turn", "turn", "active_turn"} {
		var turn struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw[key], &turn) == nil && turn.ID != "" {
			return turn.ID
		}
	}
	return ""
}

func streamEvent(event upstream.Event) UpstreamEvent {
	eventID := event.EventID
	if eventID == "" {
		// event_id is documented, but retaining distinct updates is safer than
		// collapsing all deltas for one item when an upstream omits it.
		eventID = event.Type + ":" + event.ItemID + ":" + payloadDigest(event.Raw)
	}
	return UpstreamEvent{ID: eventID, Type: event.Type, Version: "agents=v1", ItemID: event.ItemID, Payload: sanitizeJSON(event.Raw), ActionPayload: event.Raw, Failed: strings.Contains(strings.ToLower(event.Type), "failed")}
}

func payloadDigest(payload json.RawMessage) string {
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x", sum[:12])
}
func (a UpstreamAdapter) CancelSession(ctx context.Context, s ConnectionSecret, id string) error {
	c, e := a.client(s)
	if e != nil {
		return e
	}
	if e = c.CancelSession(ctx, id); e != nil {
		return wrappedUpstreamError{e}
	}
	return nil
}
func (a UpstreamAdapter) SubmitToolResult(ctx context.Context, s ConnectionSecret, id string, r ToolResult) error {
	c, e := a.client(s)
	if e != nil {
		return e
	}
	e = c.SubmitToolResultWithOptions(ctx, id, upstream.ToolResult{TurnID: r.TurnID, CallID: r.CallID, Success: r.Success, Output: r.Output, Error: r.Error}, upstream.EventOptions{IdempotencyKey: "tool-result-" + r.TurnID + "-" + r.CallID})
	if e != nil {
		return wrappedUpstreamError{e}
	}
	return nil
}

// ToolResultAccepted confirms an earlier ambiguous or interrupted delivery by
// matching the durable call and exact result in the provider's saved items.
func (a UpstreamAdapter) ToolResultAccepted(ctx context.Context, s ConnectionSecret, id string, r ToolResult) (bool, error) {
	c, err := a.client(s)
	if err != nil {
		return false, err
	}
	after := ""
	for pageNumber := 0; pageNumber < 1000; pageNumber++ {
		page, listErr := c.ListItems(ctx, id, upstream.ListOptions{After: after, Order: "asc", Limit: 100})
		if listErr != nil {
			return false, wrappedUpstreamError{listErr}
		}
		for _, item := range page.Data {
			if matchesToolResult(item.Raw, r) {
				return true, nil
			}
		}
		if !page.HasMore {
			return false, nil
		}
		next := page.LastID
		if next == "" && len(page.Data) > 0 {
			next = page.Data[len(page.Data)-1].ID
		}
		if next == "" || next == after {
			return false, fmt.Errorf("openai agents list items: pagination did not advance")
		}
		after = next
	}
	return false, fmt.Errorf("openai agents list items: pagination limit exceeded")
}

func matchesToolResult(raw json.RawMessage, r ToolResult) bool {
	var saved struct {
		Type          string `json:"type"`
		Status        string `json:"status"`
		TurnID        string `json:"turn_id"`
		CallID        string `json:"call_id"`
		Output, Error json.RawMessage
	}
	if json.Unmarshal(raw, &saved) != nil || saved.Type != "function_call_output" || saved.Status != "completed" || saved.TurnID != r.TurnID || saved.CallID != r.CallID {
		return false
	}
	var output, savedError string
	if len(saved.Output) == 0 || string(saved.Output) == "null" || json.Unmarshal(saved.Output, &output) != nil {
		return false
	}
	if len(saved.Error) == 0 {
		return false
	}
	if string(saved.Error) != "null" && json.Unmarshal(saved.Error, &savedError) != nil {
		return false
	}
	return (savedError == "") == r.Success && output == r.Output && savedError == r.Error
}
func (a UpstreamAdapter) DeleteSession(ctx context.Context, s ConnectionSecret, id string) error {
	c, e := a.client(s)
	if e != nil {
		return e
	}
	_, e = c.DeleteSession(ctx, id)
	if e != nil {
		return wrappedUpstreamError{e}
	}
	return nil
}
func fromUpstream(u *upstream.Session) *UpstreamSession {
	if u == nil {
		return &UpstreamSession{}
	}
	out := &UpstreamSession{ID: u.ID, Status: u.Status, HasRequiredActions: len(u.RequiredActions) > 0, Raw: sanitizeJSON(u.Raw), Readiness: "pending"}
	if u.Environment != nil {
		out.EnvironmentID = u.Environment.ID
		out.RemoteURL = u.Environment.RemoteURL
		out.Readiness = strings.ToLower(u.Environment.Status)
	}
	if out.EnvironmentID == "" {
		out.Readiness = "ready"
	}
	return out
}

type wrappedUpstreamError struct{ error }

func (w wrappedUpstreamError) Unwrap() error { return w.error }

func (w wrappedUpstreamError) AmbiguousResult() bool {
	var e *upstream.Error
	return errors.As(w.error, &e) && e.Ambiguous
}
