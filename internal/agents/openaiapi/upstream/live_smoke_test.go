package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

// Explicit opt-in: this test creates a paid upstream session and deletes it.
// It verifies the real protocol, not Everstack sandbox or product qualification.
func TestLiveAgentsAPISession(t *testing.T) {
	if os.Getenv("EVS_TEST_OPENAI_AGENTS_LIVE") != "1" {
		t.Skip("set EVS_TEST_OPENAI_AGENTS_LIVE=1 with an application key to run")
	}
	client, err := NewClient(Config{APIKey: os.Getenv("OPENAI_API_KEY"), Timeout: 3 * time.Minute})
	if err != nil {
		t.Fatal("live client configuration failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	model := os.Getenv("EVS_TEST_OPENAI_AGENTS_MODEL")
	if model == "" {
		model = "gpt-6-astra"
	}
	session, err := client.CreateSession(ctx, CreateSessionRequest{
		Agent:       Agent{Model: model, Instructions: "Respond briefly. This is a protocol smoke test."},
		Environment: json.RawMessage(`{"type":"none"}`),
		Input:       "Reply with the word ready.",
	})
	if err != nil {
		liveFailure(t, "create", err)
	}
	if session.ID == "" {
		t.Fatal("create returned no session ID")
	}
	t.Log("live session created")
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		if _, err := client.DeleteSession(cleanup, session.ID); err != nil {
			liveFailure(t, "cleanup", err)
		}
		t.Log("live session deleted")
	}()
	if _, err := client.GetSession(ctx, session.ID); err != nil {
		liveFailure(t, "retrieve", err)
	}
	// Wait for the initial turn to settle before follow-up. Recover saved session
	// state in case its completion preceded subscription.
	for {
		state, err := client.GetSession(ctx, session.ID)
		if err != nil {
			liveFailure(t, "retrieve initial turn", err)
		}
		if state.Status == "idle" {
			break
		}
		if state.Status == "failed" {
			t.Fatal("initial session failed")
		}
		select {
		case <-ctx.Done():
			t.Fatal("initial session did not settle")
		case <-time.After(time.Second):
		}
	}
	stream, err := client.Subscribe(ctx, session.ID, StreamOptions{})
	if err != nil {
		liveFailure(t, "subscribe", err)
	}
	defer stream.Close()
	if err := client.SendInput(ctx, session.ID, []InputMessage{{Role: "user", Content: []InputContent{{Type: "input_text", Text: "Reply with the word continued."}}}}); err != nil {
		liveFailure(t, "follow-up", err)
	}
	completed := false
	for !completed {
		event, err := stream.Next()
		if err != nil {
			liveFailure(t, "stream", err)
		}
		switch event.Type {
		case "agent.session.turn.completed":
			completed = true
		case "agent.session.turn.failed", "agent.session.failed", "agent.session.turn.cancelled":
			t.Fatal("live turn failed or was cancelled")
		}
	}
	items, err := client.ListItems(ctx, session.ID, ListOptions{})
	if err != nil {
		liveFailure(t, "saved items", err)
	}
	if len(items.Data) == 0 {
		t.Fatal("saved items were empty")
	}
	t.Logf("live create/retrieve/subscribe/follow-up/completion/items verified; saved items=%d", len(items.Data))
}

func liveFailure(t *testing.T, operation string, err error) {
	t.Helper()
	var upstreamError *Error
	if errors.As(err, &upstreamError) {
		t.Fatalf("live %s failed: kind=%s status=%d code=%s request_id=%s", operation, upstreamError.Kind, upstreamError.StatusCode, upstreamError.Code, upstreamError.RequestID)
	}
	t.Fatalf("live %s failed (%T)", operation, err)
}
