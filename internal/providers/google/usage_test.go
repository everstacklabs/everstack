package google

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/services/catalog"
	"github.com/everstacklabs/everstack/internal/telemetry/metrics"
)

// Recorded-shape generateContent bodies (gemini-2.5-flash).
const (
	unaryNoThoughts = `{
  "candidates": [{"content": {"parts": [{"text": "Paris."}], "role": "model"}, "finishReason": "STOP", "index": 0}],
  "usageMetadata": {
    "promptTokenCount": 8,
    "candidatesTokenCount": 3,
    "totalTokenCount": 11,
    "promptTokensDetails": [{"modality": "TEXT", "tokenCount": 8}]
  },
  "modelVersion": "gemini-2.5-flash",
  "responseId": "kq3oaO2ZJuqIz7IPkJ2-qQo"
}`
	unaryWithThoughts = `{
  "candidates": [{"content": {"parts": [{"text": "The answer is 42."}], "role": "model"}, "finishReason": "STOP", "index": 0}],
  "usageMetadata": {
    "promptTokenCount": 2100,
    "cachedContentTokenCount": 2048,
    "candidatesTokenCount": 9,
    "totalTokenCount": 3131,
    "promptTokensDetails": [{"modality": "TEXT", "tokenCount": 2100}],
    "cacheTokensDetails": [{"modality": "TEXT", "tokenCount": 2048}],
    "thoughtsTokenCount": 1022
  },
  "modelVersion": "gemini-2.5-flash",
  "responseId": "lq3oaO2ZJuqIz7IPkJ2-qQo"
}`
)

// Recorded-shape streamGenerateContent?alt=sse bodies. Every chunk carries a
// cumulative usageMetadata; the last one is complete.
const (
	streamNoThoughts = "data: {\"candidates\": [{\"content\": {\"parts\": [{\"text\": \"Par\"}],\"role\": \"model\"},\"index\": 0}],\"usageMetadata\": {\"promptTokenCount\": 8,\"totalTokenCount\": 8,\"promptTokensDetails\": [{\"modality\": \"TEXT\",\"tokenCount\": 8}]},\"modelVersion\": \"gemini-2.0-flash\",\"responseId\": \"a1\"}\r\n\r\n" +
		"data: {\"candidates\": [{\"content\": {\"parts\": [{\"text\": \"is.\"}],\"role\": \"model\"},\"finishReason\": \"STOP\",\"index\": 0}],\"usageMetadata\": {\"promptTokenCount\": 8,\"candidatesTokenCount\": 3,\"totalTokenCount\": 11,\"promptTokensDetails\": [{\"modality\": \"TEXT\",\"tokenCount\": 8}],\"candidatesTokensDetails\": [{\"modality\": \"TEXT\",\"tokenCount\": 3}]},\"modelVersion\": \"gemini-2.0-flash\",\"responseId\": \"a1\"}\r\n\r\n"
	streamWithThoughts = "data: {\"candidates\": [{\"content\": {\"parts\": [{\"text\": \"The answer\"}],\"role\": \"model\"},\"index\": 0}],\"usageMetadata\": {\"promptTokenCount\": 12,\"totalTokenCount\": 1034,\"promptTokensDetails\": [{\"modality\": \"TEXT\",\"tokenCount\": 12}],\"thoughtsTokenCount\": 1022},\"modelVersion\": \"gemini-2.5-flash\",\"responseId\": \"b1\"}\r\n\r\n" +
		"data: {\"candidates\": [{\"content\": {\"parts\": [{\"text\": \" is 42.\"}],\"role\": \"model\"},\"finishReason\": \"STOP\",\"index\": 0}],\"usageMetadata\": {\"promptTokenCount\": 12,\"candidatesTokenCount\": 9,\"totalTokenCount\": 1043,\"promptTokensDetails\": [{\"modality\": \"TEXT\",\"tokenCount\": 12}],\"thoughtsTokenCount\": 1022},\"modelVersion\": \"gemini-2.5-flash\",\"responseId\": \"b1\"}\r\n\r\n"
)

const usageCatalogYAML = `
providers:
  google:
    name: google
    models:
      - name: gemini-2.5-flash
        display_name: Gemini 2.5 Flash
        input_cost_per_1k: 0.0003
        output_cost_per_1k: 0.0025
        cache_read_cost_per_1k: 0.00003
`

const usageProvidersYAML = `
providers:
  google:
    name: google
`

const (
	inRate        = 0.0003 / 1000
	outRate       = 0.0025 / 1000
	cacheReadRate = 0.00003 / 1000
)

func geminiServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if strings.Contains(r.URL.Path, ":streamGenerateContent") {
			w.Header().Set("Content-Type", "text/event-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func chatUsage(t *testing.T, body string) gw.Usage {
	t.Helper()
	srv := geminiServer(t, body)
	p := NewProvider(Config{APIKey: "test", BaseURL: srv.URL})
	resp, err := p.Chat(context.Background(), gw.ChatCompletionRequest{
		Model:    "gemini-2.5-flash",
		Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text("hi"))},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	return resp.Usage
}

// streamUsage mirrors how the tracing and billing middlewares read a stream:
// the last chunk that carries usage wins.
func streamUsage(t *testing.T, body string) gw.Usage {
	t.Helper()
	srv := geminiServer(t, body)
	p := NewProvider(Config{APIKey: "test", BaseURL: srv.URL})
	var last *gw.Usage
	err := p.ChatStream(context.Background(), gw.ChatCompletionRequest{
		Model:    "gemini-2.5-flash",
		Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text("hi"))},
	}, func(c gw.ChatResponseChunk) error {
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

// cost prices usage exactly as the billing and tracing middlewares do.
func cost(t *testing.T, u gw.Usage) float64 {
	t.Helper()
	cache := catalog.NewCache()
	if err := cache.Load([]byte(usageCatalogYAML), []byte(usageProvidersYAML)); err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	calc := metrics.NewCostCalculatorFromCache(cache)
	d := calc.CalculateCostWithCacheTokens("google", "gemini-2.5-flash", u.PromptTokens, u.CompletionTokens, u.CacheReadCount(), u.CacheWriteCount())
	if !d.Priceable {
		t.Fatal("gemini-2.5-flash not priceable in test catalog")
	}
	return d.EstimatedUSD
}

func assertCost(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("cost = %.12f, want %.12f", got, want)
	}
}

func TestChatUsageWithoutThoughts(t *testing.T) {
	u := chatUsage(t, unaryNoThoughts)
	if u.PromptTokens != 8 || u.CompletionTokens != 3 || u.TotalTokens != 11 {
		t.Fatalf("tokens = %d/%d/%d, want 8/3/11", u.PromptTokens, u.CompletionTokens, u.TotalTokens)
	}
	if u.CompletionDetails != nil {
		t.Fatalf("CompletionDetails = %+v, want nil", u.CompletionDetails)
	}
	assertCost(t, cost(t, u), 8*inRate+3*outRate)
}

func TestChatUsageBillsThoughtsAtOutputRate(t *testing.T) {
	u := chatUsage(t, unaryWithThoughts)
	if u.PromptTokens != 2100 || u.CompletionTokens != 9+1022 || u.TotalTokens != 3131 {
		t.Fatalf("tokens = %d/%d/%d, want 2100/1031/3131", u.PromptTokens, u.CompletionTokens, u.TotalTokens)
	}
	if u.CompletionDetails == nil || u.CompletionDetails.ReasoningTokens != 1022 {
		t.Fatalf("CompletionDetails = %+v, want reasoning_tokens 1022", u.CompletionDetails)
	}
	if u.CacheReadCount() != 2048 {
		t.Fatalf("CacheReadCount = %d, want 2048", u.CacheReadCount())
	}
	// Thoughts are priced once, at the output rate, alongside candidates.
	assertCost(t, cost(t, u), (2100-2048)*inRate+2048*cacheReadRate+(9+1022)*outRate)
}

func TestChatStreamUsageWithoutThoughts(t *testing.T) {
	u := streamUsage(t, streamNoThoughts)
	if u.PromptTokens != 8 || u.CompletionTokens != 3 || u.TotalTokens != 11 {
		t.Fatalf("tokens = %d/%d/%d, want 8/3/11", u.PromptTokens, u.CompletionTokens, u.TotalTokens)
	}
	if u.CompletionDetails != nil {
		t.Fatalf("CompletionDetails = %+v, want nil", u.CompletionDetails)
	}
	assertCost(t, cost(t, u), 8*inRate+3*outRate)
}

func TestChatStreamUsageBillsThoughtsAtOutputRate(t *testing.T) {
	u := streamUsage(t, streamWithThoughts)
	if u.PromptTokens != 12 || u.CompletionTokens != 9+1022 || u.TotalTokens != 1043 {
		t.Fatalf("tokens = %d/%d/%d, want 12/1031/1043", u.PromptTokens, u.CompletionTokens, u.TotalTokens)
	}
	if u.CompletionDetails == nil || u.CompletionDetails.ReasoningTokens != 1022 || u.CompletionDetails.TextTokens != 9 {
		t.Fatalf("CompletionDetails = %+v, want reasoning 1022 text 9", u.CompletionDetails)
	}
	assertCost(t, cost(t, u), 12*inRate+(9+1022)*outRate)
}
