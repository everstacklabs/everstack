package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

func TestReviewImagesAndStopSequences(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "unary", true: "stream"}[stream], func(t *testing.T) {
			called := false
			cli := &http.Client{Transport: toolsTransport(func(r *http.Request) (*http.Response, error) {
				called = true
				var wire map[string]any
				if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
					t.Fatal(err)
				}
				stops := wire["stop_sequences"].([]any)
				if len(stops) != 2 || stops[0] != "END" || stops[1] != "DONE" {
					t.Fatal(stops)
				}
				msgs := wire["messages"].([]any)
				if len(msgs) != 1 {
					t.Fatal(msgs)
				}
				parts := msgs[0].(map[string]any)["content"].([]any)
				if len(parts) != 3 {
					t.Fatal(parts)
				}
				for i, p := range parts {
					block := p.(map[string]any)
					source := block["source"].(map[string]any)
					if block["type"] != "image" {
						t.Fatal(block)
					}
					if i == 0 {
						if source["type"] != "base64" || source["media_type"] != "image/png" || source["data"] != "aW1hZ2U=" {
							t.Fatal(source)
						}
					} else if source["type"] != "url" {
						t.Fatal(source)
					}
				}
				body := `{"id":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"stop_sequence","usage":{"input_tokens":2,"output_tokens":1}}`
				if stream {
					body = "data: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"model\":\"claude\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"stop_sequence\"},\"usage\":{\"output_tokens\":1}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			p := NewProvider(Config{BaseURL: "http://upstream.invalid", APIKey: "test"})
			p.client = cli
			p.unaryClient = cli
			req := gw.ChatCompletionRequest{Model: "claude", Sampling: gw.SamplingParams{Stop: []string{"END", "DONE"}}, Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.ImageURL("data:image/png;base64,aW1hZ2U="), gw.ImageURL("https://image.example/a.png"), gw.ImageURL("http://image.example/b.png"))}}
			if stream {
				if err := p.ChatStream(context.Background(), req, func(c gw.ChatResponseChunk) error {
					for _, d := range c.Choices {
						if d.FinishReason != "stop" {
							t.Fatal(d)
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			} else {
				resp, err := p.Chat(context.Background(), req)
				if err != nil || resp.Choices[0].FinishReason != "stop" {
					t.Fatal(resp, err)
				}
			}
			if !called {
				t.Fatal("no upstream call")
			}
		})
	}
}
func TestReviewImageCacheBreakpointAndInvalidSources(t *testing.T) {
	p := NewProvider(Config{APIKey: "test"})
	wire, err := p.toClaude(gw.ChatCompletionRequest{Metadata: map[string]any{"cache_control": "ephemeral"}, Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.ImageURL("https://image.example/a"))}})
	if err != nil {
		t.Fatal(err)
	}
	image := wire.Messages[0].Content[0].(claudeImageContent)
	if image.CacheControl == nil {
		t.Fatal("image cache breakpoint dropped")
	}
	for _, source := range []string{"data:image/png,raw", "ftp://image.example/a"} {
		if _, err := anthropicImage(source); err == nil {
			t.Fatal("unsupported source accepted", source)
		}
	}
}
