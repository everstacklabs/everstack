package observability

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type memoryRepository struct {
	turns map[string]TurnUsage
	err   error
}

func (m *memoryRepository) RecordTurnUsage(_ context.Context, u TurnUsage) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	if old, ok := m.turns[u.UpstreamTurnID]; ok && old.Cursor >= u.Cursor {
		return false, nil
	}
	m.turns[u.UpstreamTurnID] = u
	return true, nil
}
func (m *memoryRepository) SessionUsage(context.Context, Scope) (UsageSnapshot, error) {
	return Unavailable(), m.err
}

func TestRecorderUsesAuthoritativeTurnSnapshots(t *testing.T) {
	repo := &memoryRepository{turns: map[string]TurnUsage{}}
	recorder, _ := NewRecorder(repo)
	base := Event{
		Scope:             Scope{OrganizationID: "org-a", InstanceID: "instance-a", LocalSessionID: "local-a"},
		UpstreamSessionID: "session-a", EventID: "event-1", Cursor: 10,
		Payload: json.RawMessage(`{"turn":{"id":"turn-a","usage":{"input_tokens":8,"total_tokens":8}}}`),
	}
	changed, err := recorder.Observe(context.Background(), base)
	if err != nil || !changed {
		t.Fatalf("first observation changed=%v err=%v", changed, err)
	}
	if got := repo.turns["turn-a"].InputTokens; got == nil || *got != 8 {
		t.Fatalf("stored input tokens = %v", got)
	}
	base.EventID, base.Cursor = "event-2", 9
	base.Payload = json.RawMessage(`{"turn_id":"turn-a","usage":{"input_tokens":999}}`)
	changed, err = recorder.Observe(context.Background(), base)
	if err != nil || changed || *repo.turns["turn-a"].InputTokens != 8 {
		t.Fatalf("stale snapshot changed=%v value=%d err=%v", changed, *repo.turns["turn-a"].InputTokens, err)
	}
}

func TestRecorderPreservesSparseReportedZero(t *testing.T) {
	repo := &memoryRepository{turns: map[string]TurnUsage{}}
	recorder, _ := NewRecorder(repo)
	changed, err := recorder.Observe(context.Background(), Event{
		Scope:             Scope{OrganizationID: "o", InstanceID: "i", LocalSessionID: "l"},
		UpstreamSessionID: "s", EventID: "e", Cursor: 1,
		Payload: json.RawMessage(`{"turn":{"id":"t"},"usage":{"output_tokens":0}}`),
	})
	got := repo.turns["t"]
	if err != nil || !changed || got.InputTokens != nil || got.OutputTokens == nil || *got.OutputTokens != 0 || got.TotalTokens != nil {
		t.Fatalf("sparse zero was not preserved: %#v, changed=%v err=%v", got, changed, err)
	}
}

func TestRecorderIgnoresSessionUsageWithoutTurn(t *testing.T) {
	repo := &memoryRepository{turns: map[string]TurnUsage{}}
	recorder, _ := NewRecorder(repo)
	changed, err := recorder.Observe(context.Background(), Event{
		Scope:             Scope{OrganizationID: "o", InstanceID: "i", LocalSessionID: "l"},
		UpstreamSessionID: "s", EventID: "e", Cursor: 1,
		Payload: json.RawMessage(`{"session":{"id":"s","usage":{"total_tokens":20}},"usage":{"total_tokens":20}}`),
	})
	if err != nil || changed || len(repo.turns) != 0 {
		t.Fatalf("session usage was recorded: changed=%v err=%v", changed, err)
	}
}

func TestTelemetryDoesNotRecordPayloadOrErrorText(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
	secret := "sk-secret-must-not-appear"
	repo := &memoryRepository{turns: map[string]TurnUsage{}, err: errors.New(secret)}
	recorder, _ := NewRecorder(repo)
	_, _ = recorder.Observe(context.Background(), Event{
		Scope:             Scope{OrganizationID: "org", InstanceID: "instance", LocalSessionID: "local"},
		UpstreamSessionID: "upstream", EventID: "event", Cursor: 1,
		Payload: json.RawMessage(`{"turn_id":"turn","usage":{"total_tokens":1},"prompt":"private","api_key":"` + secret + `"}`),
	})
	b, _ := json.Marshal(exporter.GetSpans())
	if string(b) == "" || contains(string(b), secret) || contains(string(b), "private") {
		t.Fatalf("span output leaked sensitive content: %s", b)
	}
}

func TestUnavailableHasNoInventedTimestampOrTokens(t *testing.T) {
	got := Unavailable()
	if got.Status != UsageUnavailable || got.Source != SourceOpenAI || got.InputTokens != nil || got.OutputTokens != nil || got.TotalTokens != nil || got.UpdatedAt != nil {
		t.Fatalf("unexpected unavailable snapshot: %#v", got)
	}
	b, _ := json.Marshal(got)
	if string(b) != `{"status":"unavailable","source":"openai"}` {
		t.Fatalf("unavailable JSON = %s", b)
	}
}

func contains(value, part string) bool {
	for i := 0; i+len(part) <= len(value); i++ {
		if value[i:i+len(part)] == part {
			return true
		}
	}
	return false
}
