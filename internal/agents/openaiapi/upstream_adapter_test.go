package openaiapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/agents/openaiapi/upstream"
)

func TestStreamEventsReconcilesSnapshotAndAllItemPagesWhileSubscribeBlocks(t *testing.T) {
	subscribeStarted := make(chan struct{})
	var subscribeOnce sync.Once
	var requestsMu sync.Mutex
	var itemAfters []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("OpenAI-Beta") != "agents=v1" {
			http.Error(w, "missing required headers", http.StatusBadRequest)
			return
		}
		switch {
		case r.URL.Path == "/v1/agents/sessions/session-1/events":
			subscribeOnce.Do(func() { close(subscribeStarted) })
			// Deliberately never write headers. Client.Subscribe must remain
			// blocked while snapshot reconciliation makes progress.
			<-r.Context().Done()
		case r.URL.Path == "/v1/agents/sessions/session-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"session-1","status":"requires_action","current_turn":{"id":"turn-1"},"required_actions":[{"type":"function_call","call_id":"call-1","name":"marker","arguments":{"marker":"exact-value"}}]}`))
		case r.URL.Path == "/v1/agents/sessions/session-1/items":
			after := r.URL.Query().Get("after")
			requestsMu.Lock()
			itemAfters = append(itemAfters, after)
			requestsMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if after == "" {
				_, _ = w.Write([]byte(`{"data":[{"id":"item-1","type":"message","status":"completed"}],"last_id":"item-1","has_more":true}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"item-2","type":"message","status":"completed"}],"last_id":"item-2","has_more":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	emitted := make(chan UpstreamEvent, 8)
	done := make(chan error, 1)
	adapter := UpstreamAdapter{BaseURL: server.URL + "/v1", Timeout: 5 * time.Second}
	go func() {
		done <- adapter.StreamEvents(ctx, ConnectionSecret{APIKey: "test-key"}, "session-1", func(event UpstreamEvent) error {
			emitted <- event
			return nil
		})
	}()

	select {
	case <-subscribeStarted:
	case <-time.After(time.Second):
		t.Fatal("SSE subscription was not attempted")
	}

	want := map[string]UpstreamEvent{}
	deadline := time.After(2 * time.Second)
	for len(want) < 3 {
		select {
		case event := <-emitted:
			key := event.ID
			if event.Type == "agent.session.item.recovered" {
				key = "item:" + event.ItemID
			}
			want[key] = event
		case <-deadline:
			t.Fatalf("events emitted while Subscribe blocked = %v", want)
		}
	}
	action := want["required-action:turn-1:call-1"]
	if action.ID == "" {
		t.Fatalf("required action missing: %v", want)
	}
	var actionPayload struct {
		TurnID string `json:"turn_id"`
		CallID string `json:"call_id"`
		Name   string `json:"name"`
	}
	if err := json.Unmarshal(action.ActionPayload, &actionPayload); err != nil {
		t.Fatal(err)
	}
	if actionPayload.TurnID != "turn-1" || actionPayload.CallID != "call-1" || actionPayload.Name != "marker" {
		t.Fatalf("action payload = %+v", actionPayload)
	}
	if want["item:item-1"].ID == "" || want["item:item-2"].ID == "" {
		t.Fatalf("paginated items missing: %v", want)
	}
	requestsMu.Lock()
	gotAfters := append([]string(nil), itemAfters...)
	requestsMu.Unlock()
	if len(gotAfters) < 2 || gotAfters[0] != "" || gotAfters[1] != "item-1" {
		t.Fatalf("item pagination cursors = %v", gotAfters)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("StreamEvents did not stop on cancellation")
	}
}

func TestReconcileRefetchesTailAndVersionsMutableItem(t *testing.T) {
	var mu sync.Mutex
	completed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/agents/sessions/session-1" {
			_, _ = w.Write([]byte(`{"id":"session-1","status":"active"}`))
			return
		}
		mu.Lock()
		final := completed
		mu.Unlock()
		if final {
			_, _ = w.Write([]byte(`{"data":[{"id":"same-item","type":"message","status":"completed","content":"final text"}],"last_id":"same-item","has_more":false}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"same-item","type":"message","status":"in_progress","content":""}],"last_id":"same-item","has_more":false}`))
	}))
	t.Cleanup(server.Close)
	adapter := UpstreamAdapter{BaseURL: server.URL + "/v1", Timeout: time.Second}
	client, err := adapter.client(ConnectionSecret{APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	var state itemReconcileState
	var events []UpstreamEvent
	emit := func(event UpstreamEvent) error {
		if event.Type == "agent.session.item.recovered" {
			events = append(events, event)
		}
		return nil
	}
	if err = reconcileUpstreamState(context.Background(), client, "session-1", &state, emit); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	completed = true
	mu.Unlock()
	if err = reconcileUpstreamState(context.Background(), client, "session-1", &state, emit); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].ItemID != "same-item" || events[1].ItemID != "same-item" {
		t.Fatalf("mutable item events = %+v", events)
	}
	if events[0].ID == events[1].ID {
		t.Fatalf("mutable snapshots shared event ID %q", events[0].ID)
	}
	if string(events[1].Payload) == string(events[0].Payload) {
		t.Fatalf("final snapshot did not replace initial payload: %s", events[1].Payload)
	}
}

func TestStreamEventFallbackIDPreservesDistinctDeltas(t *testing.T) {
	first := streamEvent(upstream.Event{Type: "agent.session.turn.output_text.delta", ItemID: "item-1", Raw: json.RawMessage(`{"delta":"a"}`)})
	second := streamEvent(upstream.Event{Type: "agent.session.turn.output_text.delta", ItemID: "item-1", Raw: json.RawMessage(`{"delta":"b"}`)})
	if first.ID == second.ID {
		t.Fatalf("distinct deltas shared fallback ID %q", first.ID)
	}
}

func TestStreamEventsReturnsSubscriptionErrorAfterSnapshotReconciliation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/agents/sessions/session-1/events":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"expired credential"}}`))
		case "/v1/agents/sessions/session-1":
			_, _ = w.Write([]byte(`{"id":"session-1","status":"active"}`))
		case "/v1/agents/sessions/session-1/items":
			_, _ = w.Write([]byte(`{"data":[{"id":"saved-item","type":"message","status":"completed"}],"last_id":"saved-item","has_more":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	var emitted []UpstreamEvent
	adapter := UpstreamAdapter{BaseURL: server.URL + "/v1", Timeout: time.Second}
	err := adapter.StreamEvents(context.Background(), ConnectionSecret{APIKey: "test-key"}, "session-1", func(event UpstreamEvent) error {
		emitted = append(emitted, event)
		return nil
	})
	var upstreamErr *upstream.Error
	if !errors.As(err, &upstreamErr) || upstreamErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("subscription error = %T %v", err, err)
	}
	if len(emitted) != 1 || emitted[0].ItemID != "saved-item" {
		t.Fatalf("snapshot was not reconciled before returning stream error: %+v", emitted)
	}
}

func TestStreamEventsReturnsReconciliationError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/agents/sessions/session-1/events" {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"revoked"}}`))
	}))
	t.Cleanup(server.Close)

	adapter := UpstreamAdapter{BaseURL: server.URL + "/v1", Timeout: time.Second}
	err := adapter.StreamEvents(context.Background(), ConnectionSecret{APIKey: "test-key"}, "session-1", func(UpstreamEvent) error { return nil })
	var upstreamErr *upstream.Error
	if !errors.As(err, &upstreamErr) || upstreamErr.StatusCode != http.StatusForbidden {
		t.Fatalf("reconciliation error = %T %v", err, err)
	}
}
