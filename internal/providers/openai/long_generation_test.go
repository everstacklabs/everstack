package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	clientx "github.com/everstacklabs/everstack/internal/providers/httpclient"
)

// generationDelay exceeds both ceilings that used to bound a unary completion:
// the 12s per-request context deadline and the shared transport's 30s
// ResponseHeaderTimeout. A non-streaming upstream sends no response headers
// until the completion is finished, so a slow generation is indistinguishable
// from a slow upstream from the transport's point of view.
const generationDelay = 35 * time.Second

// newSlowCompletionServer emulates an upstream generating a long answer: it
// withholds headers for generationDelay, then returns a well-formed response.
func newSlowCompletionServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(generationDelay):
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl-slow",
			"model": "gpt-5.5",
			"choices": [{"index":0,"message":{"role":"assistant","content":"a long answer"},"finish_reason":"stop"}],
			"usage": {"prompt_tokens":10,"completion_tokens":900,"total_tokens":910}
		}`))
	}))
}

// TestChatCompletesLongGeneration is the regression test for the deadline
// defect: a request whose *output* is long must succeed. It failed
// deterministically before, at 12.1s, regardless of how small the input was.
func TestChatCompletesLongGeneration(t *testing.T) {
	if testing.Short() {
		t.Skip("takes longer than the generation delay it is exercising")
	}

	srv := newSlowCompletionServer(t)
	defer srv.Close()

	// No HTTPClient override: the provider must build the clients it would use
	// in production, otherwise the transport ceiling under test is bypassed.
	p := NewProvider(Config{APIKey: "test-key", BaseURL: srv.URL})

	start := time.Now()
	resp, err := p.Chat(context.Background(), gw.ChatCompletionRequest{
		Model:    "gpt-5.5",
		Messages: []gw.Message{{Role: gw.RoleUser, Content: []gw.ContentPart{gw.Text("draft a long document")}}},
		Sampling: gw.SamplingParams{MaxTokens: 4000},
	})
	if err != nil {
		t.Fatalf("Chat() failed after %v: %v", time.Since(start), err)
	}
	if len(resp.Choices) == 0 || len(resp.Choices[0].Message.Content) == 0 {
		t.Fatalf("Chat() returned no content: %+v", resp)
	}
	if elapsed := time.Since(start); elapsed < generationDelay {
		t.Fatalf("server withheld headers for %v but Chat returned in %v; the stub is not exercising the ceiling", generationDelay, elapsed)
	}
}

// TestDefaultClientStillGuardsTimeToFirstByte pins down why the unary client
// exists at all. The shared streaming client keeps its ResponseHeaderTimeout,
// which is the correct guard for a stream (headers arrive at first token) and
// the wrong one for a unary completion.
func TestDefaultClientStillGuardsTimeToFirstByte(t *testing.T) {
	if testing.Short() {
		t.Skip("takes longer than the generation delay it is exercising")
	}

	srv := newSlowCompletionServer(t)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clientx.Default().Do(req); err == nil {
		t.Fatal("Default() should still time out awaiting response headers; its guard was weakened")
	}

	req2, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := clientx.Unary().Do(req2)
	if err != nil {
		t.Fatalf("Unary() must not cap generation time: %v", err)
	}
	defer resp.Body.Close()
}
