package mistral

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

func upstream(t *testing.T, status int, contentType, payload string) (*Provider, *map[string]any) {
	t.Helper()
	got := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		fmt.Fprint(w, payload)
	}))
	t.Cleanup(srv.Close)
	return NewProvider(Config{BaseURL: srv.URL, APIKey: "k"}), &got
}

func userReq(s gw.SamplingParams) gw.ChatCompletionRequest {
	return gw.ChatCompletionRequest{Model: "mistral-large", Sampling: s, Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text("x"))}}
}

const okChat = `{"id":"c","model":"mistral-large","choices":[{"message":{"content":"hi"},"finish_reason":"length"}],` +
	`"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`

func TestChatCapStopFinishAndCache(t *testing.T) {
	p, got := upstream(t, 200, "application/json", okChat)
	resp, err := p.Chat(context.Background(), userReq(gw.SamplingParams{MaxCompletionTokens: 50, MaxTokens: 400, Stop: []string{"END"}}))
	if err != nil {
		t.Fatal(err)
	}
	if (*got)["max_tokens"] != float64(50) {
		t.Fatalf("max_tokens = %v", (*got)["max_tokens"])
	}
	if _, has := (*got)["max_completion_tokens"]; has {
		t.Fatal("max_completion_tokens must not be sent")
	}
	if stop, _ := (*got)["stop"].([]any); len(stop) != 1 || stop[0] != "END" {
		t.Fatalf("stop = %v", (*got)["stop"])
	}
	if resp.Choices[0].FinishReason != "length" {
		t.Fatalf("finish = %q", resp.Choices[0].FinishReason)
	}
}

func TestStreamFinishUsageAndRequest(t *testing.T) {
	p, got := upstream(t, 200, "text/event-stream",
		"data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"a\"}}]}\n\n"+
			"data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"length\"}]}\n\n"+
			"data: {\"id\":\"c\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":1,\"total_tokens\":11}}\n\n"+
			"data: [DONE]\n\n")
	var finish string
	var usage *gw.Usage
	err := p.ChatStream(context.Background(), userReq(gw.SamplingParams{MaxCompletionTokens: 9, Stop: []string{"S"}}), func(c gw.ChatResponseChunk) error {
		if c.Usage != nil {
			usage = c.Usage
		}
		for _, ch := range c.Choices {
			if ch.FinishReason != "" {
				finish = ch.FinishReason
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if finish != "length" {
		t.Fatalf("an empty-content final chunk must still carry finish_reason, got %q", finish)
	}
	if usage == nil || usage.PromptTokens != 10 {
		t.Fatalf("usage = %+v", usage)
	}
	if (*got)["max_tokens"] != float64(9) || (*got)["stop"] == nil {
		t.Fatalf("request = %v", *got)
	}
}

func TestUpstreamErrorsAreTyped(t *testing.T) {
	p, _ := upstream(t, 429, "application/json", `{"error":{"message":"slow down","type":"rate_limit"}}`)
	check := func(path string, err error) {
		t.Helper()
		var ue *gw.UpstreamError
		if !errors.As(err, &ue) || ue.StatusCode != 429 || ue.Provider != "mistral" {
			t.Fatalf("%s: %v (%T)", path, err, err)
		}
	}
	_, err := p.Chat(context.Background(), userReq(gw.SamplingParams{}))
	check("chat", err)
	check("stream", p.ChatStream(context.Background(), userReq(gw.SamplingParams{}), func(gw.ChatResponseChunk) error { return nil }))
	_, err = p.Embed(context.Background(), gw.EmbeddingsRequest{Model: "m", Input: "x"})
	check("embed", err)
}

func TestEmbedEmptyIsError(t *testing.T) {
	p, _ := upstream(t, 200, "application/json", `{"data":[]}`)
	if _, err := p.Embed(context.Background(), gw.EmbeddingsRequest{Model: "m", Input: "x"}); err == nil {
		t.Fatal("empty embedding must error")
	}
}

func TestTruncatedStreamIsAnError(t *testing.T) {
	p, _ := upstream(t, 200, "text/event-stream", "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"par\"}}]}\n\n")
	if err := p.ChatStream(context.Background(), userReq(gw.SamplingParams{}), func(gw.ChatResponseChunk) error { return nil }); err == nil {
		t.Fatal("truncated stream must error")
	}
	p, _ = upstream(t, 200, "text/event-stream", "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
	if err := p.ChatStream(context.Background(), userReq(gw.SamplingParams{}), func(gw.ChatResponseChunk) error { return nil }); err != nil {
		t.Fatalf("finish_reason without DONE is a clean end: %v", err)
	}
}

func TestImagesReachTheModel(t *testing.T) {
	p, got := upstream(t, 200, "application/json", okChat)
	img := "data:image/png;base64,AAAA"
	txt := "what is this?"
	req := gw.ChatCompletionRequest{Model: "pixtral", Messages: []gw.Message{
		gw.NewMessage(gw.RoleUser, gw.Text("plain")),
		{Role: gw.RoleUser, Content: []gw.ContentPart{{Type: "text", Text: &txt}, {Type: "image_url", ImageURL: &img}}},
	}}
	if _, err := p.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	msgs := (*got)["messages"].([]any)
	if msgs[0].(map[string]any)["content"] != "plain" {
		t.Fatalf("text-only must stay a string: %v", msgs[0])
	}
	parts, ok := msgs[1].(map[string]any)["content"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("content = %v", msgs[1])
	}
	// Mistral documents image_url as a string (ImageURLChunk).
	if c := parts[1].(map[string]any); c["type"] != "image_url" || c["image_url"] != img {
		t.Fatalf("image chunk = %v", c)
	}
}
