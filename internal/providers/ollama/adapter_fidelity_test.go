package ollama

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

func upstream(t *testing.T, status int, payload string) (*Provider, *map[string]any) {
	t.Helper()
	got := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		w.WriteHeader(status)
		fmt.Fprint(w, payload)
	}))
	t.Cleanup(srv.Close)
	return NewProvider(Config{BaseURL: srv.URL}), &got
}

func userReq(s gw.SamplingParams) gw.ChatCompletionRequest {
	return gw.ChatCompletionRequest{Model: "llama3", Sampling: s, Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text("x"))}}
}

const okChat = `{"model":"llama3","created_at":"t","message":{"role":"assistant","content":"hi"},"done":true,"done_reason":"length","prompt_eval_count":26,"eval_count":9}`

func TestChatOptionsFinishAndUsage(t *testing.T) {
	p, got := upstream(t, 200, okChat)
	resp, err := p.Chat(context.Background(), userReq(gw.SamplingParams{MaxCompletionTokens: 40, MaxTokens: 90, Stop: []string{"END"}}))
	if err != nil {
		t.Fatal(err)
	}
	// https://github.com/ollama/ollama/blob/main/docs/api.md: options.num_predict, options.stop.
	opts, _ := (*got)["options"].(map[string]any)
	if opts["num_predict"] != float64(40) {
		t.Fatalf("options = %v", opts)
	}
	if stop, _ := opts["stop"].([]any); len(stop) != 1 || stop[0] != "END" {
		t.Fatalf("stop = %v", opts["stop"])
	}
	if resp.Choices[0].FinishReason != "length" {
		t.Fatalf("finish = %q", resp.Choices[0].FinishReason)
	}
	if u := resp.Usage; u.PromptTokens != 26 || u.CompletionTokens != 9 || u.TotalTokens != 35 {
		t.Fatalf("usage = %+v", u)
	}
}

func TestImagesUseNativeImagesField(t *testing.T) {
	p, got := upstream(t, 200, okChat)
	img := "data:image/png;base64,QUJD"
	txt := "describe"
	req := gw.ChatCompletionRequest{Model: "llava", Messages: []gw.Message{
		{Role: gw.RoleUser, Content: []gw.ContentPart{{Type: "text", Text: &txt}, {Type: "image_url", ImageURL: &img}}},
	}}
	if _, err := p.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	m := (*got)["messages"].([]any)[0].(map[string]any)
	imgs, _ := m["images"].([]any)
	if m["content"] != "describe" || len(imgs) != 1 || imgs[0] != "QUJD" {
		t.Fatalf("message = %v (Ollama wants raw base64 in images)", m)
	}
}

func TestRemoteImageURLIsRejected(t *testing.T) {
	p, _ := upstream(t, 200, okChat)
	u := "https://example.com/a.png"
	req := gw.ChatCompletionRequest{Model: "llava", Messages: []gw.Message{
		{Role: gw.RoleUser, Content: []gw.ContentPart{{Type: "image_url", ImageURL: &u}}},
	}}
	if _, err := p.Chat(context.Background(), req); err == nil {
		t.Fatal("Ollama only takes base64 images; a remote URL must be an error, not silently dropped")
	}
}

func TestStreamFinishAndUsageOnDone(t *testing.T) {
	p, _ := upstream(t, 200,
		`{"model":"llama3","message":{"content":"a"},"done":false}`+"\n"+
			`{"model":"llama3","message":{"content":""},"done":true,"done_reason":"length","prompt_eval_count":5,"eval_count":2}`+"\n")
	var finish string
	var usage *gw.Usage
	err := p.ChatStream(context.Background(), userReq(gw.SamplingParams{}), func(c gw.ChatResponseChunk) error {
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
	if finish != "length" || usage == nil || usage.PromptTokens != 5 || usage.CompletionTokens != 2 {
		t.Fatalf("finish=%q usage=%+v", finish, usage)
	}
}

func TestStreamToolCallFinishesOnce(t *testing.T) {
	p, _ := upstream(t, 200,
		`{"model":"llama3","message":{"tool_calls":[{"function":{"name":"f","arguments":{"a":1}}}]},"done":false}`+"\n"+
			`{"model":"llama3","message":{"content":""},"done":true,"done_reason":"stop"}`+"\n")
	var finishes []string
	err := p.ChatStream(context.Background(), userReq(gw.SamplingParams{}), func(c gw.ChatResponseChunk) error {
		for _, ch := range c.Choices {
			if ch.FinishReason != "" {
				finishes = append(finishes, ch.FinishReason)
			}
		}
		return nil
	})
	if err != nil || len(finishes) != 1 || finishes[0] != "tool_calls" {
		t.Fatalf("finishes=%v err=%v", finishes, err)
	}
}

func TestUpstreamErrorsAreTyped(t *testing.T) {
	p, _ := upstream(t, 404, `{"error":"model 'llama3' not found"}`)
	check := func(path string, err error) {
		t.Helper()
		var ue *gw.UpstreamError
		if !errors.As(err, &ue) || ue.StatusCode != 404 || ue.Provider != "ollama" || ue.Message == "" {
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
	p, _ := upstream(t, 200, `{"embedding":[]}`)
	if _, err := p.Embed(context.Background(), gw.EmbeddingsRequest{Model: "m", Input: "x"}); err == nil {
		t.Fatal("empty embedding must error")
	}
}

func TestTruncatedStreamIsAnError(t *testing.T) {
	p, _ := upstream(t, 200, `{"model":"llama3","message":{"content":"par"},"done":false}`+"\n")
	if err := p.ChatStream(context.Background(), userReq(gw.SamplingParams{}), func(gw.ChatResponseChunk) error { return nil }); err == nil {
		t.Fatal("a stream that ends before done:true must error")
	}
}
