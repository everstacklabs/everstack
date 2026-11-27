package deepseek

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// Streams ask for the final usage chunk so platform-key streams are billed
// on reported tokens, not an estimate.
func TestChatStreamRequestsUsage(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()
	p := NewProvider(Config{APIKey: "k", BaseURL: srv.URL})
	_ = p.ChatStream(context.Background(), gw.ChatCompletionRequest{Model: "m", Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text("hi"))}},
		func(gw.ChatResponseChunk) error { return nil })
	opts, _ := body["stream_options"].(map[string]any)
	if opts["include_usage"] != true {
		t.Fatalf("stream request must set stream_options.include_usage: %v", body)
	}
}
