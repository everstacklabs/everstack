package openai_compatible

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// Streams ask for usage (stream_options.include_usage), so the billing seam
// sees real token counts from every OpenAI-compatible upstream that honours
// it; an upstream that rejects the field is retried once without it.
func TestChatStreamRequestsUsage(t *testing.T) {
	for _, rejects := range []bool{false, true} {
		t.Run(fmt.Sprintf("rejects=%v", rejects), func(t *testing.T) {
			var mu sync.Mutex
			var bodies []map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				var body map[string]any
				_ = json.Unmarshal(raw, &body)
				mu.Lock()
				bodies = append(bodies, body)
				mu.Unlock()
				if _, has := body["stream_options"]; has && rejects {
					http.Error(w, `{"error":{"message":"Unrecognized request argument supplied: stream_options"}}`, http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n")
				fmt.Fprint(w, "data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":3,\"total_tokens\":15}}\n\n")
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer srv.Close()

			p := NewProvider(Config{ProviderName: "groq", BaseURL: srv.URL, APIKey: "k"})
			var usage *gw.Usage
			err := p.ChatStream(context.Background(), gw.ChatCompletionRequest{Model: "m", Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text("hello"))}},
				func(c gw.ChatResponseChunk) error {
					if c.Usage != nil {
						usage = c.Usage
					}
					return nil
				})
			if err != nil {
				t.Fatal(err)
			}
			if usage == nil || usage.PromptTokens != 12 || usage.CompletionTokens != 3 {
				t.Fatalf("usage = %+v", usage)
			}
			opts, _ := bodies[0]["stream_options"].(map[string]any)
			if opts["include_usage"] != true {
				t.Fatalf("first request must ask for usage: %v", bodies[0])
			}
			if !rejects && len(bodies) != 1 {
				t.Fatalf("requests = %d", len(bodies))
			}
			if rejects {
				if len(bodies) != 2 {
					t.Fatalf("a rejection must be retried once without stream_options: %d requests", len(bodies))
				}
				if _, has := bodies[1]["stream_options"]; has {
					t.Fatal("retry must drop stream_options")
				}
			}
		})
	}
}
