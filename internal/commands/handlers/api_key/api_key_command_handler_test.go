package api_key

import (
	"context"
	"encoding/json"
	"testing"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

func TestCreateEventUsesCommandIDAndSurfaces(t *testing.T) {
	for _, surfaces := range [][]string{nil, {}, {"mcp"}} {
		cmd := NewCreateApiKeyCommand("bot:test", "API_KEY_TYPE_ORG", "one-time-secret", "user", "tenant", "")
		cmd.Surfaces = surfaces
		events, err := NewApiKeyCommandHandler().Handle(contextkeys.WithAPIKeyHashSecret(context.Background(), "test-secret"), cmd)
		if err != nil || len(events) != 1 {
			t.Fatalf("events: %v", err)
		}
		var payload struct {
			ID       string   `json:"id"`
			Surfaces []string `json:"surfaces"`
			Hash     string   `json:"hash"`
		}
		if err = json.Unmarshal(events[0].Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.ID != cmd.ID {
			t.Fatalf("stored id %s differs from returned/billing id %s", payload.ID, cmd.ID)
		}
		if (payload.Surfaces == nil) != (surfaces == nil) || len(payload.Surfaces) != len(surfaces) {
			t.Fatal("surface set lost")
		}
		if payload.Hash == "" || payload.Hash == cmd.Plaintext {
			t.Fatal("key was not hashed")
		}
	}
}
