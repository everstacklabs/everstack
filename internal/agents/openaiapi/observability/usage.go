package observability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const SourceOpenAI = "openai"

type UsageStatus string

const (
	UsageUnavailable UsageStatus = "unavailable"
	UsageReported    UsageStatus = "reported"
)

// Scope is supplied from the authenticated request. None of these values are
// accepted from an upstream event payload.
type Scope struct {
	OrganizationID string
	InstanceID     string
	LocalSessionID string
}

// UsageSnapshot contains only values actually reported by OpenAI. A non-nil
// pointer whose value is zero is a reported zero; nil means unavailable.
type UsageSnapshot struct {
	Status       UsageStatus `json:"status"`
	InputTokens  *int64      `json:"input_tokens,omitempty"`
	OutputTokens *int64      `json:"output_tokens,omitempty"`
	TotalTokens  *int64      `json:"total_tokens,omitempty"`
	UpdatedAt    *time.Time  `json:"updated_at,omitempty"`
	Source       string      `json:"source"`
}

func Unavailable() UsageSnapshot {
	return UsageSnapshot{Status: UsageUnavailable, Source: SourceOpenAI}
}

// Event is the narrow, redacted-safe input to the usage recorder. Payload is
// parsed only for turn identity and token counts and is never traced or stored
// by this package.
type Event struct {
	Scope
	UpstreamSessionID string
	EventID           string
	Cursor            int64
	ObservedAt        time.Time
	Payload           json.RawMessage
}

type TurnUsage struct {
	Scope
	UpstreamSessionID string
	UpstreamTurnID    string
	EventID           string
	Cursor            int64
	InputTokens       *int64
	OutputTokens      *int64
	TotalTokens       *int64
	ObservedAt        time.Time
}

type Repository interface {
	RecordTurnUsage(context.Context, TurnUsage) (bool, error)
	SessionUsage(context.Context, Scope) (UsageSnapshot, error)
}

type Recorder struct {
	repository Repository
	tracer     trace.Tracer
}

func NewRecorder(repository Repository) (*Recorder, error) {
	if repository == nil {
		return nil, errors.New("OpenAI usage repository is required")
	}
	return &Recorder{repository: repository, tracer: otel.Tracer("everstack.openai-agents.usage")}, nil
}

// Observe records only authoritative turn usage. Session-level cumulative
// usage and events without a turn ID are deliberately ignored because adding
// those values to per-turn usage would double count provider charges.
func (r *Recorder) Observe(ctx context.Context, event Event) (bool, error) {
	ctx, span := r.tracer.Start(ctx, "openai.agents.usage.observe", trace.WithAttributes(
		attribute.String("openai.organization_id", event.OrganizationID),
		attribute.String("openai.instance_id", event.InstanceID),
		attribute.String("openai.local_session_id", event.LocalSessionID),
		attribute.String("openai.upstream_session_id", event.UpstreamSessionID),
		attribute.String("openai.event_id", event.EventID),
		attribute.String("openai.usage.source", SourceOpenAI),
	))
	defer span.End()

	usage, ok, err := parseTurnUsage(event)
	if err != nil {
		span.SetStatus(codes.Error, "invalid provider usage")
		return false, err
	}
	if !ok {
		return false, nil
	}
	span.SetAttributes(attribute.String("openai.upstream_turn_id", usage.UpstreamTurnID))
	changed, err := r.repository.RecordTurnUsage(ctx, usage)
	if err != nil {
		// Do not RecordError: repository errors may include driver text or values.
		span.SetStatus(codes.Error, "usage persistence failed")
		return false, err
	}
	return changed, nil
}

func (r *Recorder) Snapshot(ctx context.Context, scope Scope) (UsageSnapshot, error) {
	ctx, span := r.tracer.Start(ctx, "openai.agents.usage.snapshot", trace.WithAttributes(
		attribute.String("openai.organization_id", scope.OrganizationID),
		attribute.String("openai.instance_id", scope.InstanceID),
		attribute.String("openai.local_session_id", scope.LocalSessionID),
		attribute.String("openai.usage.source", SourceOpenAI),
	))
	defer span.End()
	snapshot, err := r.repository.SessionUsage(ctx, scope)
	if err != nil {
		span.SetStatus(codes.Error, "usage lookup failed")
	}
	return snapshot, err
}

type eventEnvelope struct {
	TurnID string `json:"turn_id"`
	Turn   *struct {
		ID    string      `json:"id"`
		Usage *usageValue `json:"usage"`
	} `json:"turn"`
	Usage *usageValue `json:"usage"`
}

type usageValue struct {
	InputTokens  *int64 `json:"input_tokens"`
	OutputTokens *int64 `json:"output_tokens"`
	TotalTokens  *int64 `json:"total_tokens"`
}

func parseTurnUsage(event Event) (TurnUsage, bool, error) {
	if event.OrganizationID == "" || event.InstanceID == "" || event.LocalSessionID == "" ||
		event.UpstreamSessionID == "" || event.EventID == "" || event.Cursor <= 0 {
		return TurnUsage{}, false, errors.New("incomplete OpenAI usage correlation")
	}
	var envelope eventEnvelope
	if len(event.Payload) == 0 || json.Unmarshal(event.Payload, &envelope) != nil {
		return TurnUsage{}, false, nil
	}
	turnID := strings.TrimSpace(envelope.TurnID)
	usage := envelope.Usage
	if envelope.Turn != nil {
		if turnID == "" {
			turnID = strings.TrimSpace(envelope.Turn.ID)
		}
		if envelope.Turn.Usage != nil {
			usage = envelope.Turn.Usage
		}
	}
	if turnID == "" || usage == nil || (usage.InputTokens == nil && usage.OutputTokens == nil && usage.TotalTokens == nil) {
		return TurnUsage{}, false, nil
	}
	for _, value := range []*int64{usage.InputTokens, usage.OutputTokens, usage.TotalTokens} {
		if value != nil && *value < 0 {
			return TurnUsage{}, false, fmt.Errorf("provider reported negative token usage")
		}
	}
	return TurnUsage{
		Scope: event.Scope, UpstreamSessionID: event.UpstreamSessionID,
		UpstreamTurnID: turnID, EventID: event.EventID, Cursor: event.Cursor,
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, TotalTokens: usage.TotalTokens,
		ObservedAt: event.ObservedAt,
	}, true, nil
}
