package deepseek

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
	return gw.ChatCompletionRequest{Model: "deepseek-chat", Sampling: s, Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text("x"))}}
}

const okChat = `{"id":"c","model":"deepseek-chat","choices":[{"message":{"content":"hi"},"finish_reason":"length"}],` +
	`"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"prompt_cache_hit_tokens":7,"prompt_cache_miss_tokens":3}}`

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
	if resp.Usage.CacheReadCount() != 7 {
		t.Fatalf("cache read = %d (%+v)", resp.Usage.CacheReadCount(), resp.Usage)
	}
}

func TestStreamFinishUsageAndRequest(t *testing.T) {
	p, got := upstream(t, 200, "text/event-stream",
		"data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"a\"}}]}\n\n"+
			"data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"length\"}]}\n\n"+
			"data: {\"id\":\"c\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":1,\"total_tokens\":11,\"prompt_cache_hit_tokens\":5}}\n\n"+
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
	if usage == nil || usage.CacheReadCount() != 5 {
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
		if !errors.As(err, &ue) || ue.StatusCode != 429 || ue.Provider != "deepseek" {
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
