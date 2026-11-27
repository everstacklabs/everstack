// Package providertest holds assertions shared by the tests of thin provider
// packages that register the shared OpenAI-compatible adapter. It is imported
// only from _test.go files.
package providertest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/providers/factory"
)

// AssertRegistered pins that a provider package's init() reaches the registry,
// that lookup ignores case, that a missing key is refused, and that the built
// provider answers to its slug and to the models it was configured with.
//
// A provider package nobody blank-imports registers nothing and fails
// silently at runtime, which is the failure this exists to catch.
func AssertRegistered(t *testing.T, slug, model string) {
	t.Helper()

	build, ok := factory.Default.Get(slug)
	if !ok {
		t.Fatalf("%s is not registered in the provider factory", slug)
	}
	if _, ok := factory.Default.Get(strings.ToUpper(slug)); !ok {
		t.Fatalf("uppercase %s did not resolve", strings.ToUpper(slug))
	}

	if _, err := build(factory.AggregatedInput{Provider: slug}); err == nil {
		t.Fatalf("%s: expected an error when no API key is configured", slug)
	}

	provider, err := build(factory.AggregatedInput{
		Provider: slug,
		APIKey:   "test-key",
		Models:   []string{model},
	})
	if err != nil {
		t.Fatalf("build %s provider: %v", slug, err)
	}
	if got := provider.Name(); got != slug {
		t.Fatalf("provider name = %q, want %q", got, slug)
	}
	if !provider.SupportsModel(model) {
		t.Fatalf("%s should support configured model %q", slug, model)
	}
	if provider.SupportsModel("not-a-configured-model") {
		t.Fatalf("%s should not claim a model it was not configured with", slug)
	}
}

type capturedRequest struct {
	Method        string
	Path          string
	Authorization string
	Body          map[string]any
}

// AssertBearerChatAndStream builds the registered provider against a local
// server and proves that a chat completion and a streamed chat completion
// both POST to <base>/chat/completions with "Authorization: Bearer <key>" and
// the configured model, and that each response is decoded.
//
// basePath is the path component of the provider's documented base URL (for
// example "/v1/openai"), so the test exercises the same URL join the real host
// would see.
func AssertBearerChatAndStream(t *testing.T, slug, model, basePath string) {
	t.Helper()

	var mu sync.Mutex
	var seen []capturedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		seen = append(seen, capturedRequest{
			Method:        r.Method,
			Path:          r.URL.Path,
			Authorization: r.Header.Get("Authorization"),
			Body:          body,
		})
		mu.Unlock()

		if stream, _ := body["stream"].(bool); stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"id\":\"c1\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"content\":\"streamed\"}}]}\n\n", model)
			fmt.Fprintf(w, "data: {\"id\":\"c1\",\"model\":%q,\"choices\":[],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":1,\"total_tokens\":5}}\n\n", model)
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"c1","model":%q,"choices":[{"message":{"content":"hello back"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`, model)
	}))
	defer srv.Close()

	build, ok := factory.Default.Get(slug)
	if !ok {
		t.Fatalf("%s is not registered in the provider factory", slug)
	}
	provider, err := build(factory.AggregatedInput{
		Provider: slug,
		APIKey:   "secret-test-key",
		BaseURL:  srv.URL + basePath,
		Models:   []string{model},
	})
	if err != nil {
		t.Fatalf("build %s provider: %v", slug, err)
	}

	req := gw.ChatCompletionRequest{
		Model:    model,
		Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text("hello"))},
	}

	resp, err := provider.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("%s chat: %v", slug, err)
	}
	if len(resp.Choices) == 0 {
		t.Fatalf("%s chat returned no choices: %+v", slug, resp)
	}

	var streamed strings.Builder
	err = provider.ChatStream(context.Background(), req, func(c gw.ChatResponseChunk) error {
		for _, choice := range c.Choices {
			for _, part := range choice.Delta.Content {
				if part.Text != nil {
					streamed.WriteString(*part.Text)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%s stream: %v", slug, err)
	}
	if streamed.String() != "streamed" {
		t.Fatalf("%s stream content = %q, want %q", slug, streamed.String(), "streamed")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("%s: upstream saw %d requests, want 2 (one chat, one stream)", slug, len(seen))
	}
	wantPath := basePath + "/chat/completions"
	for i, r := range seen {
		if r.Method != http.MethodPost {
			t.Errorf("request %d method = %s, want POST", i, r.Method)
		}
		if r.Path != wantPath {
			t.Errorf("request %d path = %q, want %q", i, r.Path, wantPath)
		}
		if r.Authorization != "Bearer secret-test-key" {
			t.Errorf("request %d did not carry the bearer key", i)
		}
		if r.Body["model"] != model {
			t.Errorf("request %d model = %v, want %q", i, r.Body["model"], model)
		}
	}
	if stream, _ := seen[0].Body["stream"].(bool); stream {
		t.Errorf("chat request must not ask for a stream")
	}
	if stream, _ := seen[1].Body["stream"].(bool); !stream {
		t.Errorf("streamed request must set stream: true")
	}
}
