package openai_compatible

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// xAI reports reasoning tokens outside completion_tokens: total_tokens is
// prompt + completion + reasoning. This is the usage block from the
// GET /v1/responses example in xAI's REST reference
// (https://docs.x.ai/developers/rest-api-reference/inference/responses).
const xaiReasoningUsage = `{
	"prompt_tokens": 32,
	"completion_tokens": 9,
	"total_tokens": 151,
	"prompt_tokens_details": {"text_tokens": 32, "audio_tokens": 0, "image_tokens": 0, "cached_tokens": 8},
	"completion_tokens_details": {"reasoning_tokens": 110, "audio_tokens": 0, "accepted_prediction_tokens": 0, "rejected_prediction_tokens": 0},
	"num_sources_used": 0
}`

// OpenAI's convention: reasoning_tokens is a breakdown of completion_tokens,
// and total_tokens is prompt + completion.
const openAIReasoningUsage = `{
	"prompt_tokens": 32,
	"completion_tokens": 119,
	"total_tokens": 151,
	"completion_tokens_details": {"reasoning_tokens": 110}
}`

// Perplexity Sonar reports reasoning_tokens at the top level of usage, priced
// at its own rate, so it is not folded into completion tokens.
const perplexityUsage = `{
	"prompt_tokens": 10,
	"completion_tokens": 20,
	"total_tokens": 30,
	"reasoning_tokens": 500,
	"citation_tokens": 40
}`

func TestReasoningTokenUsage(t *testing.T) {
	cases := []struct {
		name           string
		usage          string
		wantPrompt     int
		wantCompletion int
		wantTotal      int
		wantReasoning  int
	}{
		{"xai reasoning outside completion", xaiReasoningUsage, 32, 119, 151, 110},
		{"openai reasoning inside completion", openAIReasoningUsage, 32, 119, 151, 110},
		{"perplexity top-level reasoning", perplexityUsage, 10, 20, 30, 0},
		{"no details", `{"prompt_tokens":5,"completion_tokens":7,"total_tokens":12}`, 5, 7, 12, 0},
	}
	for _, tc := range cases {
		check := func(t *testing.T, u *gw.Usage) {
			t.Helper()
			if u == nil {
				t.Fatal("no usage")
			}
			if u.PromptTokens != tc.wantPrompt || u.CompletionTokens != tc.wantCompletion || u.TotalTokens != tc.wantTotal {
				t.Fatalf("usage = prompt %d, completion %d, total %d; want %d, %d, %d",
					u.PromptTokens, u.CompletionTokens, u.TotalTokens, tc.wantPrompt, tc.wantCompletion, tc.wantTotal)
			}
			if u.PromptTokens+u.CompletionTokens != u.TotalTokens {
				t.Fatalf("prompt + completion = %d, want total %d", u.PromptTokens+u.CompletionTokens, u.TotalTokens)
			}
			got := 0
			if u.CompletionDetails != nil {
				got = u.CompletionDetails.ReasoningTokens
			}
			if got != tc.wantReasoning {
				t.Fatalf("reasoning tokens = %d, want %d", got, tc.wantReasoning)
			}
		}

		t.Run(tc.name+"/chat", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"id":"c1","model":"grok-4","choices":[{"message":{"content":"4"}}],"usage":%s}`, tc.usage)
			}))
			defer srv.Close()
			p := NewProvider(Config{ProviderName: "xai", BaseURL: srv.URL, APIKey: "k"})
			resp, err := p.Chat(context.Background(), gw.ChatCompletionRequest{Model: "grok-4", Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text("2+2"))}})
			if err != nil {
				t.Fatal(err)
			}
			check(t, &resp.Usage)
		})

		t.Run(tc.name+"/stream", func(t *testing.T) {
			var compact map[string]any
			if err := json.Unmarshal([]byte(tc.usage), &compact); err != nil {
				t.Fatal(err)
			}
			usageJSON, _ := json.Marshal(compact)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"id\":\"c1\",\"model\":\"grok-4\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"4\"}}]}\n\n")
				fmt.Fprintf(w, "data: {\"id\":\"c1\",\"model\":\"grok-4\",\"choices\":[],\"usage\":%s}\n\n", usageJSON)
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer srv.Close()
			p := NewProvider(Config{ProviderName: "xai", BaseURL: srv.URL, APIKey: "k"})
			var usage *gw.Usage
			err := p.ChatStream(context.Background(), gw.ChatCompletionRequest{Model: "grok-4", Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text("2+2"))}},
				func(c gw.ChatResponseChunk) error {
					if c.Usage != nil {
						usage = c.Usage
					}
					return nil
				})
			if err != nil {
				t.Fatal(err)
			}
			check(t, usage)
		})
	}
}
