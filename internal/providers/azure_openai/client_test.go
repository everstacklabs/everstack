package azure_openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

func TestChatUsesDeploymentPathAndTools(t *testing.T) {
	var gotPath string
	var gotBody azureChatRequest

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		if r.Header.Get("api-key") != "secret" {
			t.Fatalf("expected api-key header")
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      "chatcmpl-1",
			"created": 1710000000,
			"model":   "gpt-4o",
			"choices": []map[string]interface{}{{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": "hello back",
				},
				"finish_reason": "stop",
			}},
			"usage": map[string]interface{}{
				"prompt_tokens":     10,
				"completion_tokens": 4,
				"total_tokens":      14,
			},
		})
	}))
	defer ts.Close()

	p := NewProvider(Config{APIKey: "secret", BaseURL: ts.URL, APIVersion: "2024-10-21"})
	resp, err := p.Chat(context.Background(), gw.ChatCompletionRequest{
		Model: "gpt-4o",
		Sampling: gw.SamplingParams{
			TopP:           0,
			TopPConfigured: true,
		},
		Messages: []gw.Message{{
			Role:    gw.RoleUser,
			Content: []gw.ContentPart{gw.Text("hi")},
		}},
		Tools: []gw.ToolDefinition{{
			Type: "function",
			Function: gw.ToolFunctionDef{
				Name: "lookup",
			},
		}},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if gotPath != "/openai/deployments/gpt-4o/chat/completions?api-version=2024-10-21" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if len(gotBody.Tools) != 1 || gotBody.Tools[0].Function.Name != "lookup" {
		t.Fatalf("expected tools in request body")
	}
	if gotBody.TopP == nil || *gotBody.TopP != 0 {
		t.Fatalf("top_p = %v, want configured zero", gotBody.TopP)
	}
	if resp.Model != "gpt-4o" || len(resp.Choices) != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestEmbedUsesDeploymentPath(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{{
				"embedding": []float64{0.1, 0.2},
			}},
			"usage": map[string]interface{}{
				"prompt_tokens": 2,
				"total_tokens":  2,
			},
		})
	}))
	defer ts.Close()

	p := NewProvider(Config{APIKey: "secret", BaseURL: ts.URL, APIVersion: "2024-10-21"})
	resp, err := p.Embed(context.Background(), gw.EmbeddingsRequest{Model: "text-embedding-3-large", Input: "hi"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if gotPath != "/openai/deployments/text-embedding-3-large/embeddings?api-version=2024-10-21" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if len(resp.Embedding) != 2 || resp.Usage == nil || resp.Usage.TotalTokens != 2 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}
