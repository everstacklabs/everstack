package openai_compatible_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/providers/factory"

	_ "github.com/everstacklabs/everstack/internal/providers/cerebras"
	_ "github.com/everstacklabs/everstack/internal/providers/fireworks"
	_ "github.com/everstacklabs/everstack/internal/providers/groq"
	_ "github.com/everstacklabs/everstack/internal/providers/huggingface"
	_ "github.com/everstacklabs/everstack/internal/providers/minimax"
	_ "github.com/everstacklabs/everstack/internal/providers/moonshot"
	_ "github.com/everstacklabs/everstack/internal/providers/nvidia_nim"
	_ "github.com/everstacklabs/everstack/internal/providers/openrouter"
	_ "github.com/everstacklabs/everstack/internal/providers/perplexity"
	_ "github.com/everstacklabs/everstack/internal/providers/together"
	_ "github.com/everstacklabs/everstack/internal/providers/voyage"
	_ "github.com/everstacklabs/everstack/internal/providers/xai"
	_ "github.com/everstacklabs/everstack/internal/providers/zai"
)

// The thin wrappers only register a factory over openai_compatible. This
// builds each one through the registry against a fake upstream and drives a
// unary chat, a streamed tool call and an upstream error end to end.
func TestOpenAICompatibleWrappersRoundTrip(t *testing.T) {
	// Registry name -> provider name reported by the built provider.
	wrappers := []string{
		"cerebras", "fireworks", "groq", "huggingface", "minimax", "moonshot",
		"nvidia-nim", "openrouter", "perplexity", "together", "voyage", "xai", "zai",
	}
	for _, name := range wrappers {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
					http.NotFound(w, r)
					return
				}
				switch {
				case strings.Contains(string(body), "trigger-error"):
					w.WriteHeader(http.StatusTooManyRequests)
					fmt.Fprint(w, `{"error":{"message":"slow down","type":"rate_limit_error"}}`)
				case strings.Contains(string(body), `"stream":true`):
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"id\":\"c\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\\\"q\\\":1}\"}}]}}]}\n\n")
					fmt.Fprint(w, "data: {\"id\":\"c\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
					fmt.Fprint(w, "data: [DONE]\n\n")
				default:
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"id":"c","model":"m","choices":[{"index":0,"message":{"content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)
				}
			}))
			defer srv.Close()

			build, ok := factory.Default.Get(name)
			if !ok {
				t.Fatalf("%s is not registered", name)
			}
			provider, err := build(factory.AggregatedInput{Provider: name, APIKey: "test-key", BaseURL: srv.URL})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			ctx := context.Background()
			ask := func(text string) gw.ChatCompletionRequest {
				return gw.ChatCompletionRequest{Model: "m", Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text(text))}}
			}

			resp, err := provider.Chat(ctx, ask("ping"))
			if err != nil || len(resp.Choices) != 1 || resp.Usage.TotalTokens != 4 || resp.Choices[0].FinishReason != "stop" {
				t.Fatalf("unary chat: resp=%+v err=%v", resp, err)
			}

			var calls []gw.ToolCall
			var finish string
			err = provider.ChatStream(ctx, ask("call a tool"), func(c gw.ChatResponseChunk) error {
				for _, ch := range c.Choices {
					calls = append(calls, ch.Delta.ToolCalls...)
					if ch.FinishReason != "" {
						finish = ch.FinishReason
					}
				}
				return nil
			})
			if err != nil || len(calls) != 1 || calls[0].Function.Name != "lookup" || finish != "tool_calls" {
				t.Fatalf("stream: calls=%+v finish=%q err=%v", calls, finish, err)
			}

			_, err = provider.Chat(ctx, ask("trigger-error"))
			var ue *gw.UpstreamError
			if !errors.As(err, &ue) || ue.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("error response: %v (%T)", err, err)
			}
		})
	}
}
