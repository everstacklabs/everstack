package openai_compatible

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

func textPtr(s string) *string { return &s }

// capture starts a fake upstream that records the request body and answers
// with the given status and payload.
func capture(t *testing.T, status int, contentType, payload string) (*httptest.Server, *map[string]any) {
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
	return srv, &got
}

const okChat = `{"id":"c1","model":"m","choices":[{"index":0,"message":{"content":"hi"},"finish_reason":"length"}],` +
	`"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"prompt_tokens_details":{"cached_tokens":6}}}`

func newTestProvider(srv *httptest.Server) *Provider {
	return NewProvider(Config{ProviderName: "groq", BaseURL: srv.URL, APIKey: "k"})
}

func multimodalRequest() gw.ChatCompletionRequest {
	img := "data:image/png;base64,AAAA"
	return gw.ChatCompletionRequest{
		Model: "m",
		Messages: []gw.Message{
			gw.NewMessage(gw.RoleUser, gw.Text("plain text only")),
			{Role: gw.RoleUser, Content: []gw.ContentPart{
				{Type: "text", Text: textPtr("what is this?")},
				{Type: "image_url", ImageURL: &img},
			}},
		},
	}
}

func assertMultimodalBody(t *testing.T, body map[string]any) {
	t.Helper()
	msgs := body["messages"].([]any)
	if s, ok := msgs[0].(map[string]any)["content"].(string); !ok || s != "plain text only" {
		t.Fatalf("text-only message must stay a plain string, got %#v", msgs[0])
	}
	parts, ok := msgs[1].(map[string]any)["content"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("multimodal message must be a content-parts array, got %#v", msgs[1])
	}
	img := parts[1].(map[string]any)
	if img["type"] != "image_url" || img["image_url"].(map[string]any)["url"] != "data:image/png;base64,AAAA" {
		t.Fatalf("image part = %#v", img)
	}
}

func TestChatSendsImageParts(t *testing.T) {
	srv, got := capture(t, 200, "application/json", okChat)
	if _, err := newTestProvider(srv).Chat(context.Background(), multimodalRequest()); err != nil {
		t.Fatal(err)
	}
	assertMultimodalBody(t, *got)
}

func TestChatStreamSendsImageParts(t *testing.T) {
	srv, got := capture(t, 200, "text/event-stream", "data: [DONE]\n\n")
	if err := newTestProvider(srv).ChatStream(context.Background(), multimodalRequest(), func(gw.ChatResponseChunk) error { return nil }); err != nil {
		t.Fatal(err)
	}
	assertMultimodalBody(t, *got)
}

func TestMaxCompletionTokensIsHonoured(t *testing.T) {
	cases := []struct {
		name       string
		sampling   gw.SamplingParams
		wantTokens float64
	}{
		{"completion cap only", gw.SamplingParams{MaxCompletionTokens: 77}, 77},
		{"both set prefers completion cap", gw.SamplingParams{MaxTokens: 500, MaxCompletionTokens: 77}, 77},
		{"max_tokens only", gw.SamplingParams{MaxTokens: 33}, 33},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := capture(t, 200, "application/json", okChat)
			req := gw.ChatCompletionRequest{Model: "m", Sampling: tc.sampling, Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text("x"))}}
			if _, err := newTestProvider(srv).Chat(context.Background(), req); err != nil {
				t.Fatal(err)
			}
			if (*got)["max_tokens"] != tc.wantTokens {
				t.Fatalf("max_tokens = %v, want %v (body %v)", (*got)["max_tokens"], tc.wantTokens, *got)
			}
			if _, has := (*got)["max_completion_tokens"]; has {
				t.Fatal("max_completion_tokens must not be sent to OpenAI-compatible upstreams")
			}
		})
	}
}

func TestChatReadsFinishReasonAndCachedTokens(t *testing.T) {
	srv, _ := capture(t, 200, "application/json", okChat)
	resp, err := newTestProvider(srv).Chat(context.Background(),
		gw.ChatCompletionRequest{Model: "m", Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text("x"))}})
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Choices[0].FinishReason; got != "length" {
		t.Fatalf("finish_reason = %q, want length", got)
	}
	if got := resp.Usage.CacheReadCount(); got != 6 {
		t.Fatalf("cache read = %d, want 6 (usage %+v)", got, resp.Usage)
	}
}

func TestStreamUsageCarriesCachedTokens(t *testing.T) {
	srv, _ := capture(t, 200, "text/event-stream",
		"data: {\"id\":\"c\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"a\"},\"finish_reason\":\"stop\"}]}\n\n"+
			"data: {\"id\":\"c\",\"model\":\"m\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":1,\"total_tokens\":11,\"prompt_tokens_details\":{\"cached_tokens\":4}}}\n\n"+
			"data: [DONE]\n\n")
	var usage *gw.Usage
	var finish string
	err := newTestProvider(srv).ChatStream(context.Background(),
		gw.ChatCompletionRequest{Model: "m", Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text("x"))}},
		func(c gw.ChatResponseChunk) error {
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
	if usage == nil || usage.CacheReadCount() != 4 {
		t.Fatalf("usage = %+v", usage)
	}
	if finish != "stop" {
		t.Fatalf("finish = %q", finish)
	}
}

func TestUpstreamErrorsAreTyped(t *testing.T) {
	body := `{"error":{"message":"slow down","type":"rate_limit_error","code":"rate_limit"}}`
	for _, status := range []int{400, 429} {
		srv, _ := capture(t, status, "application/json", body)
		p := newTestProvider(srv)
		req := gw.ChatCompletionRequest{Model: "m", Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text("x"))}}

		check := func(path string, err error) {
			t.Helper()
			var ue *gw.UpstreamError
			if !errors.As(err, &ue) || ue.StatusCode != status || ue.Message != "slow down" || ue.Provider != "groq" {
				t.Fatalf("%s status %d: err = %v (%T)", path, status, err, err)
			}
		}
		_, err := p.Chat(context.Background(), req)
		check("chat", err)
		check("stream", p.ChatStream(context.Background(), req, func(gw.ChatResponseChunk) error { return nil }))
		_, err = p.Embed(context.Background(), gw.EmbeddingsRequest{Model: "m", Input: "x"})
		check("embed", err)
	}
}

func TestEmbedRejectsEmptyVector(t *testing.T) {
	for name, payload := range map[string]string{
		"no data":         `{"data":[]}`,
		"empty embedding": `{"data":[{"embedding":[]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := capture(t, 200, "application/json", payload)
			if _, err := newTestProvider(srv).Embed(context.Background(), gw.EmbeddingsRequest{Model: "m", Input: "x"}); err == nil {
				t.Fatal("an empty embedding must be an error")
			}
		})
	}
	srv, _ := capture(t, 200, "application/json", `{"data":[{"embedding":[0.5,0.25]}]}`)
	resp, err := newTestProvider(srv).Embed(context.Background(), gw.EmbeddingsRequest{Model: "m", Input: "x"})
	if err != nil || len(resp.Embedding) != 2 {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
}

func TestTruncatedStreamIsAnError(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		wantErr bool
	}{
		{"cut off mid answer", "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"par\"}}]}\n\n", true},
		{"finish_reason but no DONE", "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n", false},
		{"DONE terminates", "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := capture(t, 200, "text/event-stream", tc.payload)
			err := newTestProvider(srv).ChatStream(context.Background(),
				gw.ChatCompletionRequest{Model: "m", Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text("x"))}},
				func(gw.ChatResponseChunk) error { return nil })
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr && !strings.Contains(err.Error(), "ended") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
