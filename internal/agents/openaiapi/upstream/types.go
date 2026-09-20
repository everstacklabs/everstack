// Package upstream implements the documented OpenAI Agents API HTTP protocol.
//
// It is intentionally pinned to OpenAI-Beta: agents=v1. Streams do not replay
// missed events; callers recover by subscribing again and reconciling ListItems.
package upstream

import (
	"encoding/json"
	"fmt"
)

type ErrorKind string

const (
	ErrorInvalid         ErrorKind = "invalid"
	ErrorUnauthenticated ErrorKind = "unauthenticated"
	ErrorForbidden       ErrorKind = "forbidden"
	ErrorRateLimited     ErrorKind = "rate_limited"
	ErrorUpstream        ErrorKind = "upstream"
	ErrorTransport       ErrorKind = "transport"
	ErrorProtocol        ErrorKind = "protocol"
	ErrorUnsupported     ErrorKind = "unsupported"
)

type Error struct {
	Kind       ErrorKind
	Operation  string
	StatusCode int
	Code       string
	Message    string
	RequestID  string
	RetryAfter string
	Ambiguous  bool
	Err        error
}

func (e *Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("openai agents %s: %s", e.Operation, e.Message)
	}
	if e.Err != nil {
		return fmt.Sprintf("openai agents %s: %v", e.Operation, e.Err)
	}
	return fmt.Sprintf("openai agents %s failed", e.Operation)
}
func (e *Error) Unwrap() error { return e.Err }

// AmbiguousResult reports whether the request may have reached OpenAI even
// though no response was received. Callers must reconcile instead of retrying.
func (e *Error) AmbiguousResult() bool { return e != nil && e.Ambiguous }

type Usage struct {
	InputTokens  *int64                     `json:"input_tokens,omitempty"`
	OutputTokens *int64                     `json:"output_tokens,omitempty"`
	TotalTokens  *int64                     `json:"total_tokens,omitempty"`
	Raw          json.RawMessage            `json:"-"`
	Extra        map[string]json.RawMessage `json:"-"`
}

type Environment struct {
	ID        string                     `json:"id,omitempty"`
	Type      string                     `json:"type,omitempty"`
	Status    string                     `json:"status,omitempty"`
	RemoteURL string                     `json:"remote_url,omitempty"`
	Raw       json.RawMessage            `json:"-"`
	Extra     map[string]json.RawMessage `json:"-"`
}

type RequiredAction struct {
	Type          string                     `json:"type"`
	TurnID        string                     `json:"turn_id,omitempty"`
	CallID        string                     `json:"call_id,omitempty"`
	Name          string                     `json:"name,omitempty"`
	Arguments     json.RawMessage            `json:"arguments,omitempty"`
	EnvironmentID string                     `json:"environment_id,omitempty"`
	Raw           json.RawMessage            `json:"-"`
	Extra         map[string]json.RawMessage `json:"-"`
}

type Session struct {
	ID              string                     `json:"id"`
	Object          string                     `json:"object,omitempty"`
	Status          string                     `json:"status,omitempty"`
	Environment     *Environment               `json:"environment,omitempty"`
	RequiredActions []RequiredAction           `json:"required_actions,omitempty"`
	Usage           *Usage                     `json:"usage,omitempty"`
	CreatedAt       *int64                     `json:"created_at,omitempty"`
	Raw             json.RawMessage            `json:"-"`
	Extra           map[string]json.RawMessage `json:"-"`
}

type Agent struct {
	Model        string            `json:"model,omitempty"`
	Instructions string            `json:"instructions,omitempty"`
	Tools        []json.RawMessage `json:"tools,omitempty"`
}

type CreateSessionRequest struct {
	Agent       Agent           `json:"agent"`
	Environment json.RawMessage `json:"environment,omitempty"`
	Input       any             `json:"input,omitempty"`
}

type InputContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}
type InputMessage struct {
	Role    string         `json:"role"`
	Content []InputContent `json:"content"`
}
type ToolResult struct {
	TurnID  string `json:"turn_id"`
	CallID  string `json:"call_id"`
	Success bool   `json:"success"`
	Output  any    `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}

type Item struct {
	ID     string                     `json:"id,omitempty"`
	Type   string                     `json:"type,omitempty"`
	Status string                     `json:"status,omitempty"`
	TurnID string                     `json:"turn_id,omitempty"`
	Raw    json.RawMessage            `json:"-"`
	Extra  map[string]json.RawMessage `json:"-"`
}

type Artifact struct {
	ID            string                     `json:"id"`
	Object        string                     `json:"object,omitempty"`
	SessionID     string                     `json:"session_id,omitempty"`
	EnvironmentID string                     `json:"environment_id,omitempty"`
	TurnID        string                     `json:"turn_id,omitempty"`
	Path          string                     `json:"path,omitempty"`
	SizeBytes     *int64                     `json:"size_bytes,omitempty"`
	CreatedAt     *int64                     `json:"created_at,omitempty"`
	Raw           json.RawMessage            `json:"-"`
	Extra         map[string]json.RawMessage `json:"-"`
}
type Deleted struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
	Object  string `json:"object,omitempty"`
}

type ListOptions struct {
	After  string
	Before string
	Order  string
	Limit  int
}
type Page[T any] struct {
	Object  string                     `json:"object,omitempty"`
	Data    []T                        `json:"data"`
	FirstID string                     `json:"first_id,omitempty"`
	LastID  string                     `json:"last_id,omitempty"`
	HasMore bool                       `json:"has_more"`
	Raw     json.RawMessage            `json:"-"`
	Extra   map[string]json.RawMessage `json:"-"`
}
type StreamOptions struct{ Cursor string }

// EventOptions applies to the documented session events endpoint. OpenAI
// supports Idempotency-Key here, but does not document it for session creation.
type EventOptions struct{ IdempotencyKey string }

type Event struct {
	Type    string                     `json:"type"`
	EventID string                     `json:"event_id,omitempty"`
	Session *Session                   `json:"session,omitempty"`
	Turn    json.RawMessage            `json:"turn,omitempty"`
	ItemID  string                     `json:"item_id,omitempty"`
	Delta   string                     `json:"delta,omitempty"`
	Text    string                     `json:"text,omitempty"`
	Usage   *Usage                     `json:"usage,omitempty"`
	Raw     json.RawMessage            `json:"-"`
	Extra   map[string]json.RawMessage `json:"-"`
}

func preserve(data []byte, out any, known ...string) (json.RawMessage, map[string]json.RawMessage, error) {
	if err := json.Unmarshal(data, out); err != nil {
		return nil, nil, err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, nil, err
	}
	for _, key := range known {
		delete(all, key)
	}
	if len(all) == 0 {
		all = nil
	}
	return append(json.RawMessage(nil), data...), all, nil
}

func (s *Session) UnmarshalJSON(b []byte) error {
	type plain Session
	var p plain
	raw, x, e := preserve(b, &p, "id", "object", "status", "environment", "required_actions", "usage", "created_at")
	*s = Session(p)
	s.Raw = raw
	s.Extra = x
	return e
}
func (e *Environment) UnmarshalJSON(b []byte) error {
	type plain Environment
	var p plain
	raw, x, err := preserve(b, &p, "id", "type", "status", "remote_url")
	*e = Environment(p)
	e.Raw = raw
	e.Extra = x
	return err
}
func (u *Usage) UnmarshalJSON(b []byte) error {
	type plain Usage
	var p plain
	raw, x, e := preserve(b, &p, "input_tokens", "output_tokens", "total_tokens")
	*u = Usage(p)
	u.Raw = raw
	u.Extra = x
	return e
}
func (a *RequiredAction) UnmarshalJSON(b []byte) error {
	type plain RequiredAction
	var p plain
	raw, x, e := preserve(b, &p, "type", "turn_id", "call_id", "name", "arguments", "environment_id")
	*a = RequiredAction(p)
	a.Raw = raw
	a.Extra = x
	return e
}
func (i *Item) UnmarshalJSON(b []byte) error {
	type plain Item
	var p plain
	raw, x, e := preserve(b, &p, "id", "type", "status", "turn_id")
	*i = Item(p)
	i.Raw = raw
	i.Extra = x
	return e
}
func (a *Artifact) UnmarshalJSON(b []byte) error {
	type plain Artifact
	var p plain
	raw, x, e := preserve(b, &p, "id", "object", "session_id", "environment_id", "turn_id", "path", "size_bytes", "created_at")
	*a = Artifact(p)
	a.Raw = raw
	a.Extra = x
	return e
}
func (e *Event) UnmarshalJSON(b []byte) error {
	type plain Event
	var p plain
	raw, x, err := preserve(b, &p, "type", "event_id", "session", "turn", "item_id", "delta", "text", "usage")
	*e = Event(p)
	e.Raw = raw
	e.Extra = x
	return err
}
