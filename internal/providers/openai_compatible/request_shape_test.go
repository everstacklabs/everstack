package openai_compatible

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"sync"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// Hosts such as SambaNova reject request fields they do not know with HTTP
// 400, and Meta rejects stop. This pins every top-level field the shared
// adapter sends for a plain chat request and a plain streamed one, with no
// sampling options set, so a new default field is a deliberate change. It
// also pins that a text-only message is sent as a plain string; image parts
// switch the content to a parts array (see the wire tests).
func TestPlainRequestTopLevelFields(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		if body["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"c1","model":"m","choices":[{"message":{"content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer srv.Close()

	req := gw.ChatCompletionRequest{
		Model: "m",
		Messages: []gw.Message{
			gw.NewMessage(gw.RoleUser, gw.Text("hello")),
		},
	}

	p := NewProvider(Config{ProviderName: "sambanova", BaseURL: srv.URL, APIKey: "k"})
	if _, err := p.Chat(context.Background(), req); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if err := p.ChatStream(context.Background(), req, func(gw.ChatResponseChunk) error { return nil }); err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	if len(bodies) != 2 {
		t.Fatalf("requests = %d, want 2", len(bodies))
	}
	want := [][]string{
		{"messages", "model"},
		{"messages", "model", "stream", "stream_options"},
	}
	for i, body := range bodies {
		keys := make([]string, 0, len(body))
		for key := range body {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if !reflect.DeepEqual(keys, want[i]) {
			t.Errorf("request %d top-level fields = %v, want %v", i, keys, want[i])
		}
		messages, _ := body["messages"].([]any)
		if len(messages) != 1 {
			t.Fatalf("request %d messages = %v", i, body["messages"])
		}
		message, _ := messages[0].(map[string]any)
		if !reflect.DeepEqual(message, map[string]any{"role": "user", "content": "hello"}) {
			t.Errorf("request %d message = %v, want text-only string content", i, message)
		}
	}
}
