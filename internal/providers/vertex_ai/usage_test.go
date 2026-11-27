package vertex_ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"golang.org/x/oauth2"
)

// Recorded-shape Vertex AI generateContent bodies (gemini-2.5-pro).
const (
	vertexUnaryNoThoughts = `{
  "candidates": [{"content": {"role": "model", "parts": [{"text": "Paris."}]}, "finishReason": "STOP", "avgLogprobs": -0.01}],
  "usageMetadata": {
    "promptTokenCount": 8,
    "candidatesTokenCount": 3,
    "totalTokenCount": 11,
    "trafficType": "ON_DEMAND",
    "promptTokensDetails": [{"modality": "TEXT", "tokenCount": 8}],
    "candidatesTokensDetails": [{"modality": "TEXT", "tokenCount": 3}]
  },
  "modelVersion": "gemini-2.0-flash-001",
  "createTime": "2026-10-12T09:00:00.000000Z",
  "responseId": "v1"
}`
	vertexUnaryWithThoughts = `{
  "candidates": [{"content": {"role": "model", "parts": [{"text": "The answer is 42."}]}, "finishReason": "STOP"}],
  "usageMetadata": {
    "promptTokenCount": 2100,
    "cachedContentTokenCount": 2048,
    "candidatesTokenCount": 50,
    "toolUsePromptTokenCount": 300,
    "thoughtsTokenCount": 200,
    "totalTokenCount": 2650,
    "trafficType": "ON_DEMAND",
    "promptTokensDetails": [{"modality": "TEXT", "tokenCount": 2100}],
    "cacheTokensDetails": [{"modality": "TEXT", "tokenCount": 2048}],
    "candidatesTokensDetails": [{"modality": "TEXT", "tokenCount": 50}],
    "toolUsePromptTokensDetails": [{"modality": "TEXT", "tokenCount": 300}]
  },
  "modelVersion": "gemini-2.5-pro",
  "createTime": "2026-10-12T09:00:00.000000Z",
  "responseId": "v2"
}`
	vertexStreamNoThoughts = "data: {\"candidates\": [{\"content\": {\"role\": \"model\",\"parts\": [{\"text\": \"Par\"}]}}],\"usageMetadata\": {\"trafficType\": \"ON_DEMAND\"},\"modelVersion\": \"gemini-2.0-flash-001\",\"responseId\": \"s1\"}\r\n\r\n" +
		"data: {\"candidates\": [{\"content\": {\"role\": \"model\",\"parts\": [{\"text\": \"is.\"}]},\"finishReason\": \"STOP\"}],\"usageMetadata\": {\"promptTokenCount\": 8,\"candidatesTokenCount\": 3,\"totalTokenCount\": 11,\"trafficType\": \"ON_DEMAND\"},\"modelVersion\": \"gemini-2.0-flash-001\",\"responseId\": \"s1\"}\r\n\r\n"
	vertexStreamWithThoughts = "data: {\"candidates\": [{\"content\": {\"role\": \"model\",\"parts\": [{\"text\": \"The answer\"}]}}],\"usageMetadata\": {\"trafficType\": \"ON_DEMAND\"},\"modelVersion\": \"gemini-2.5-pro\",\"responseId\": \"s2\"}\r\n\r\n" +
		"data: {\"candidates\": [{\"content\": {\"role\": \"model\",\"parts\": [{\"text\": \" is 42.\"}]},\"finishReason\": \"STOP\"}],\"usageMetadata\": {\"promptTokenCount\": 12,\"candidatesTokenCount\": 9,\"thoughtsTokenCount\": 1022,\"totalTokenCount\": 1043,\"trafficType\": \"ON_DEMAND\"},\"modelVersion\": \"gemini-2.5-pro\",\"responseId\": \"s2\"}\r\n\r\n"
)

func vertexProvider(t *testing.T, body string) *Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	p, err := NewProvider(Config{
		BaseURL:     srv.URL + "/v1/projects/demo/locations/us-central1",
		TokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tok"}),
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	return p
}

func vertexReq() gw.ChatCompletionRequest {
	return gw.ChatCompletionRequest{
		Model:    "gemini-2.5-pro",
		Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text("hi"))},
	}
}

func vertexChatUsage(t *testing.T, body string) gw.Usage {
	t.Helper()
	resp, err := vertexProvider(t, body).Chat(context.Background(), vertexReq())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	return resp.Usage
}

func vertexStreamUsage(t *testing.T, body string) gw.Usage {
	t.Helper()
	var last *gw.Usage
	err := vertexProvider(t, body).ChatStream(context.Background(), vertexReq(), func(c gw.ChatResponseChunk) error {
		if c.Usage != nil {
			u := *c.Usage
			last = &u
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if last == nil {
		t.Fatal("stream reported no usage")
	}
	return *last
}

func TestVertexChatUsageWithoutThoughts(t *testing.T) {
	u := vertexChatUsage(t, vertexUnaryNoThoughts)
	if u.PromptTokens != 8 || u.CompletionTokens != 3 || u.TotalTokens != 11 {
		t.Fatalf("tokens = %d/%d/%d, want 8/3/11", u.PromptTokens, u.CompletionTokens, u.TotalTokens)
	}
	if u.CompletionDetails != nil {
		t.Fatalf("CompletionDetails = %+v, want nil", u.CompletionDetails)
	}
}

func TestVertexChatUsageCountsThoughtsCacheAndToolUse(t *testing.T) {
	u := vertexChatUsage(t, vertexUnaryWithThoughts)
	if u.PromptTokens != 2100+300 || u.CompletionTokens != 50+200 || u.TotalTokens != 2650 {
		t.Fatalf("tokens = %d/%d/%d, want 2400/250/2650", u.PromptTokens, u.CompletionTokens, u.TotalTokens)
	}
	if u.CompletionDetails == nil || u.CompletionDetails.ReasoningTokens != 200 || u.CompletionDetails.TextTokens != 50 {
		t.Fatalf("CompletionDetails = %+v, want reasoning 200 text 50", u.CompletionDetails)
	}
	if u.CacheReadCount() != 2048 {
		t.Fatalf("CacheReadCount = %d, want 2048", u.CacheReadCount())
	}
}

func TestVertexChatStreamUsageWithoutThoughts(t *testing.T) {
	u := vertexStreamUsage(t, vertexStreamNoThoughts)
	if u.PromptTokens != 8 || u.CompletionTokens != 3 || u.TotalTokens != 11 {
		t.Fatalf("tokens = %d/%d/%d, want 8/3/11", u.PromptTokens, u.CompletionTokens, u.TotalTokens)
	}
}

func TestVertexChatStreamUsageCountsThoughts(t *testing.T) {
	u := vertexStreamUsage(t, vertexStreamWithThoughts)
	if u.PromptTokens != 12 || u.CompletionTokens != 9+1022 || u.TotalTokens != 1043 {
		t.Fatalf("tokens = %d/%d/%d, want 12/1031/1043", u.PromptTokens, u.CompletionTokens, u.TotalTokens)
	}
	if u.CompletionDetails == nil || u.CompletionDetails.ReasoningTokens != 1022 {
		t.Fatalf("CompletionDetails = %+v, want reasoning 1022", u.CompletionDetails)
	}
}
