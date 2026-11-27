package anthropic

import (
	"context"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLiveFinishOnlyThenUsage(t *testing.T) {
	cli := &http.Client{Transport: toolsTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`data: {"type":"message_start","message":{"id":"c","model":"m","usage":{"input_tokens":11}}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hel"}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}

data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}

data: {"type":"message_stop"}

`))}, nil
	})}
	p := NewProvider(Config{BaseURL: "http://upstream.invalid", APIKey: "sk-test"})
	p.client = cli
	var text strings.Builder
	finish := ""
	var usage *gw.Usage
	finishBeforeUsage := false
	err := p.ChatStream(context.Background(), gw.ChatCompletionRequest{Model: "gpt-4o", Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text("hi"))}}, func(c gw.ChatResponseChunk) error {
		for _, choice := range c.Choices {
			for _, part := range choice.Delta.Content {
				if part.Text != nil {
					text.WriteString(*part.Text)
				}
			}
			if choice.FinishReason != "" {
				finish = choice.FinishReason
			}
		}
		if c.Usage != nil {
			usage = c.Usage
			finishBeforeUsage = finish == "stop"
		}
		return nil
	})
	if err != nil || text.String() != "hello" || finish != "stop" || usage == nil || usage.PromptTokens != 11 || usage.CompletionTokens != 7 || !finishBeforeUsage {
		t.Fatalf("text=%q finish=%s usage=%+v err=%v", text.String(), finish, usage, err)
	}
}
