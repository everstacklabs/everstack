package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/providers/tracing"
	"github.com/everstacklabs/everstack/internal/services/catalog"
)

// unsupportedParamsCatalog has a Responses-routed and a Chat Completions model
// that both reject sampling controls, and one that declares nothing.
func unsupportedParamsCatalog(t *testing.T) *catalog.Cache {
	t.Helper()
	models := []byte(`providers:
  openai:
    name: openai
    models:
      - name: gpt-6.2-sol
        api_type: responses
        unsupported_parameters: [temperature, top_p]
      - name: gpt-6.2-astra
        unsupported_parameters: [temperature, top_p]
      - name: gpt-4o
`)
	providers := []byte("providers:\n  openai:\n    name: openai\n")
	cache := catalog.NewCache()
	if err := cache.Load(models, providers); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return cache
}

func samplingWithTemperatureAndTopP() gw.SamplingParams {
	topK := 40
	enabled := true
	return gw.SamplingParams{
		Temperature:           0.7,
		TemperatureConfigured: true,
		TopP:                  0.9,
		TopPConfigured:        true,
		TopK:                  &topK,
		ReasoningEnabled:      &enabled,
		ReasoningEffort:       "high",
		MaxTokens:             128,
	}
}

// captureOpenAIBody answers both /chat/completions and /responses and records
// the path and decoded body of the last call.
func captureOpenAIBody(t *testing.T, path *string, body *map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(body); err != nil {
			t.Errorf("decode upstream body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/responses" {
			_ = json.NewEncoder(w).Encode(oaResponsesResponse{
				ID: "resp-1", Model: "m", Status: "completed",
				Output: []oaResponsesOutputItem{{
					ID: "msg-1", Type: "message", Role: "assistant",
					Content: []oaResponsesOutputContent{{Type: "output_text", Text: "ok"}},
				}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "chatcmpl-1", "created": 1700000000, "model": "m",
			"choices": []map[string]interface{}{{
				"index":         0,
				"message":       map[string]interface{}{"role": "assistant", "content": "ok"},
				"finish_reason": "stop",
			}},
		})
	}))
}

func TestChatDropsCatalogUnsupportedParametersOnBothEndpoints(t *testing.T) {
	cache := unsupportedParamsCatalog(t)
	tests := []struct {
		model    string
		wantPath string
	}{
		{model: "gpt-6.2-sol", wantPath: "/responses"},
		{model: "gpt-6.2-astra", wantPath: "/chat/completions"},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			var path string
			var body map[string]interface{}
			server := captureOpenAIBody(t, &path, &body)
			defer server.Close()

			base := NewProvider(Config{
				APIKey:          "sk-test-key",
				BaseURL:         server.URL,
				APITypeForModel: catalogAPITypeLookup(cache),
			})
			provider := tracing.NewMiddlewareWithKey(base, cache, "", "", "")
			_, err := provider.Chat(t.Context(), gw.ChatCompletionRequest{
				Model:    tt.model,
				Messages: []gw.Message{{Role: gw.RoleUser, Content: []gw.ContentPart{gw.Text("hi")}}},
				Sampling: samplingWithTemperatureAndTopP(),
			})
			if err != nil {
				t.Fatalf("Chat() error = %v", err)
			}
			if path != tt.wantPath {
				t.Fatalf("path = %s, want %s", path, tt.wantPath)
			}
			for _, field := range []string{"temperature", "top_p", "top_k"} {
				if _, present := body[field]; present {
					t.Errorf("upstream body still has %q: %v", field, body)
				}
			}
			// reasoning_effort is never filtered.
			if tt.wantPath == "/chat/completions" && body["reasoning_effort"] != "high" {
				t.Errorf("reasoning_effort = %v, want high", body["reasoning_effort"])
			}
			if tt.wantPath == "/responses" {
				if reasoning, _ := body["reasoning"].(map[string]interface{}); reasoning["effort"] != "high" {
					t.Errorf("reasoning = %v, want effort high", body["reasoning"])
				}
			}
		})
	}
}

func TestChatKeepsSamplingWhenCatalogDeclaresNone(t *testing.T) {
	cache := unsupportedParamsCatalog(t)
	var path string
	var body map[string]interface{}
	server := captureOpenAIBody(t, &path, &body)
	defer server.Close()

	req := gw.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []gw.Message{{Role: gw.RoleUser, Content: []gw.ContentPart{gw.Text("hi")}}},
		Sampling: samplingWithTemperatureAndTopP(),
	}
	base := NewProvider(Config{APIKey: "sk-test-key", BaseURL: server.URL, APITypeForModel: catalogAPITypeLookup(cache)})
	if _, err := tracing.NewMiddlewareWithKey(base, cache, "", "", "").Chat(t.Context(), req); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	// Exactly the payload the adapter built before this change.
	encoded, _ := json.Marshal(toOAChatRequest(req))
	var want map[string]interface{}
	_ = json.Unmarshal(encoded, &want)
	gotJSON, _ := json.Marshal(body)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("upstream body changed:\n got  %s\n want %s", gotJSON, wantJSON)
	}
	if body["temperature"] != 0.7 || body["top_p"] != 0.9 {
		t.Fatalf("temperature/top_p = %v/%v, want 0.7/0.9", body["temperature"], body["top_p"])
	}
}

func TestRequiresMaxCompletionTokens(t *testing.T) {
	tests := map[string]bool{
		"o1":            true,
		"o1-preview":    true,
		"o3-mini":       true,
		"gpt-5":         true,
		"gpt-5.5":       true,
		"gpt-6":         true,
		"gpt-6.2-sol":   true,
		"gpt-6.2-astra": true,
		"gpt-4o":        false,
		"gpt-4.1":       false,
		"o3":            true,
		"o4-mini":       true,
	}
	for model, want := range tests {
		if got := requiresMaxCompletionTokens(model); got != want {
			t.Errorf("requiresMaxCompletionTokens(%q) = %v, want %v", model, got, want)
		}
	}
}

func TestChatCompletionsSendsMaxCompletionTokensForGPT6(t *testing.T) {
	for model, wantCompletionField := range map[string]bool{"gpt-6.2-astra": true, "gpt-5": true, "gpt-4o": false} {
		encoded, err := json.Marshal(toOAChatRequest(gw.ChatCompletionRequest{
			Model:    model,
			Sampling: gw.SamplingParams{MaxTokens: 300},
		}))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var body map[string]interface{}
		_ = json.Unmarshal(encoded, &body)
		_, hasCompletion := body["max_completion_tokens"]
		_, hasMax := body["max_tokens"]
		if hasCompletion != wantCompletionField || hasMax == wantCompletionField {
			t.Errorf("%s: body = %s, want max_completion_tokens=%v", model, encoded, wantCompletionField)
		}
	}
}
