package google

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
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`data: {"candidates":[{"content":{"parts":[{"text":"hel"}]}}]}

data: {"candidates":[{"content":{"parts":[{"text":"lo"}]}}]}

data: {"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}]}

data: {"candidates":[],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":7,"totalTokenCount":18}}

data: [DONE]

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
