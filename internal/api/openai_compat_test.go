package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestProtoErrorToOpenAI(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		fallback   int
		wantStatus int
		wantMsg    string
		wantOK     bool
	}{
		{
			name:       "gateway error middleware shape",
			body:       `{"error":{"code":5,"message":"model not found: gpt-x","type":"error"}}`,
			fallback:   http.StatusOK,
			wantStatus: http.StatusNotFound,
			wantMsg:    "model not found: gpt-x",
			wantOK:     true,
		},
		{
			name:       "grpc-gateway bare status",
			body:       `{"code":16,"message":"Invalid API key"}`,
			fallback:   http.StatusInternalServerError,
			wantStatus: http.StatusUnauthorized,
			wantMsg:    "Invalid API key",
			wantOK:     true,
		},
		{
			name:     "normal completion body is not an error",
			body:     `{"result":{"id":"chat_1","choices":[]}}`,
			fallback: http.StatusOK,
			wantOK:   false,
		},
		{
			name:     "non-json passthrough",
			body:     "data: [DONE]",
			fallback: http.StatusOK,
			wantOK:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, status, ok := protoErrorToOpenAI([]byte(tc.body), tc.fallback)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d", status, tc.wantStatus)
			}
			var parsed struct {
				Error struct {
					Message string `json:"message"`
					Type    string `json:"type"`
				} `json:"error"`
			}
			if err := json.Unmarshal(out, &parsed); err != nil {
				t.Fatalf("output not JSON: %v", err)
			}
			if parsed.Error.Message != tc.wantMsg {
				t.Fatalf("message = %q, want %q", parsed.Error.Message, tc.wantMsg)
			}
			if parsed.Error.Type == "" {
				t.Fatal("error.type missing")
			}
		})
	}
}

func TestGatewayModelsToOpenAI(t *testing.T) {
	in := `{"providers":[{"provider":"openai","models":["gpt-5.5","gpt-5.2-codex"]},{"provider":"anthropic","models":["claude-sonnet-5","gpt-5.5"]}]}`
	out, ok := gatewayModelsToOpenAI([]byte(in))
	if !ok {
		t.Fatal("conversion failed")
	}
	var parsed struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}
	if parsed.Object != "list" {
		t.Fatalf("object = %q, want list", parsed.Object)
	}
	// gpt-5.5 deduped across providers
	if len(parsed.Data) != 3 {
		t.Fatalf("len(data) = %d, want 3", len(parsed.Data))
	}
	if parsed.Data[0].ID != "gpt-5.5" || parsed.Data[0].OwnedBy != "openai" || parsed.Data[0].Object != "model" {
		t.Fatalf("unexpected first entry: %+v", parsed.Data[0])
	}
}

func TestProtoEmbeddingsToOpenAI(t *testing.T) {
	in := `{"result":{"object":"list","data":[{"object":"embedding","embedding":[0.1,0.2],"index":0}],"model":"text-embedding-3-small","usage":{"prompt_tokens":2,"total_tokens":2}}}
{"result":{}}`
	out, ok := protoEmbeddingsToOpenAI([]byte(in))
	if !ok {
		t.Fatal("conversion failed")
	}
	var parsed struct {
		Object string `json:"object"`
		Model  string `json:"model"`
		Data   []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Usage map[string]interface{} `json:"usage"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}
	if parsed.Object != "list" || parsed.Model != "text-embedding-3-small" {
		t.Fatalf("unexpected envelope: %+v", parsed)
	}
	if len(parsed.Data) != 1 || len(parsed.Data[0].Embedding) != 2 {
		t.Fatalf("unexpected data: %+v", parsed.Data)
	}
	if parsed.Usage["total_tokens"].(float64) != 2 {
		t.Fatalf("usage not carried: %+v", parsed.Usage)
	}
}

func TestSSESniffWriterStreamingPassthrough(t *testing.T) {
	rec := httptest.NewRecorder()
	s := &sseSniffWriter{w: rec}
	s.Header().Set("Content-Type", "text/event-stream")
	s.WriteHeader(http.StatusOK)
	if _, err := s.Write([]byte("data: {\"x\":1}\n\n")); err != nil {
		t.Fatal(err)
	}
	s.finishBuffered() // must be a no-op in streaming mode
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q", got)
	}
	if !strings.Contains(rec.Body.String(), `data: {"x":1}`) {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestSSESniffWriterBufferedFallback(t *testing.T) {
	rec := httptest.NewRecorder()
	s := &sseSniffWriter{w: rec}
	s.Header().Set("Content-Type", "application/json")
	// NDJSON delta frames the /v1 chain emits when SSE is disabled.
	frames := `{"result":{"id":"chat_1","model":"gpt-5.5","created":"1751600000","choices":[{"message":{"content":[{"text":"hel"}]}}]}}
{"result":{"id":"chat_1","choices":[{"message":{"content":[{"text":"lo"}]},"finish_reason":"stop"}]}}
`
	if _, err := s.Write([]byte(frames)); err != nil {
		t.Fatal(err)
	}
	s.finishBuffered()
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	var parsed struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, rec.Body.String())
	}
	if parsed.Object != "chat.completion" {
		t.Fatalf("object = %q", parsed.Object)
	}
	if len(parsed.Choices) != 1 || parsed.Choices[0].Message.Content != "hello" {
		t.Fatalf("choices = %+v", parsed.Choices)
	}
}

func TestProtoChatCompletionToOpenAIPreservesProviderContent(t *testing.T) {
	t.Parallel()

	frames := `{"result":{"id":"chat_1","model":"mistral-small-latest","choices":[{"message":{"content":[{"provider_json":"{\"type\":\"thinking\",\"thinking\":[{\"type\":\"text\",\"text\":\"trace\"}]}"},{"text":"final ","provider_json":"{\"type\":\"text\",\"text\":\"final \"}"}]}}]}}
{"result":{"choices":[{"message":{"content":[{"text":"answer","provider_json":"{\"type\":\"text\",\"text\":\"answer\"}"}]},"finish_reason":"stop"}]}}
`
	out, ok := protoChatCompletionToOpenAI([]byte(frames))
	if !ok {
		t.Fatal("conversion failed")
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content         string                   `json:"content"`
				ProviderContent []map[string]interface{} `json:"provider_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}
	if len(parsed.Choices) != 1 || parsed.Choices[0].Message.Content != "final answer" {
		t.Fatalf("unexpected normalized content: %+v", parsed.Choices)
	}
	providerContent := parsed.Choices[0].Message.ProviderContent
	if len(providerContent) != 3 ||
		providerContent[0]["type"] != "thinking" ||
		providerContent[1]["text"] != "final " ||
		providerContent[2]["text"] != "answer" {
		t.Fatalf("provider_content was not preserved in order: %#v", providerContent)
	}
}

func TestSSESniffWriterBufferedError(t *testing.T) {
	rec := httptest.NewRecorder()
	s := &sseSniffWriter{w: rec}
	s.Header().Set("Content-Type", "application/json")
	s.WriteHeader(http.StatusInternalServerError)
	if _, err := s.Write([]byte(`{"error":{"code":5,"message":"model not found: x","type":"error"}}`)); err != nil {
		t.Fatal(err)
	}
	s.finishBuffered()
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid_request_error") {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestTransformChatCompletionRequestReplaysProviderContent(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model": "mistral-small-latest",
		"messages": [{
			"role": "assistant",
			"content": "final answer",
			"provider_content": [
				{"type":"thinking","thinking":[{"type":"text","text":"trace"}]},
				{"type":"text","text":"final answer"}
			]
		}]
	}`))

	transformed := transformChatCompletionRequest(request)
	body, err := io.ReadAll(transformed.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	var protoRequest gatewaypb.ChatCompletionRequest
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(body, &protoRequest); err != nil {
		t.Fatalf("transformed body is not a ChatCompletionRequest: %v\n%s", err, body)
	}
	if len(protoRequest.GetMessages()) != 1 {
		t.Fatalf("messages = %d, want 1", len(protoRequest.GetMessages()))
	}
	parts := protoRequest.GetMessages()[0].GetContent()
	if len(parts) != 2 {
		t.Fatalf("content parts = %d, want 2", len(parts))
	}
	if parts[0].GetType() != "thinking" || parts[0].GetProviderJson() == "" {
		t.Fatalf("thinking chunk was not retained: %#v", parts[0])
	}
	if parts[1].GetText() != "final answer" || parts[1].GetProviderJson() == "" {
		t.Fatalf("text chunk was not retained: %#v", parts[1])
	}
}

func TestTransformChatCompletionRequestMovesSamplingWithPresence(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model": "gpt-5.6",
		"messages": [{"role": "user", "content": "hello"}],
		"temperature": 0,
		"top_p": 0,
		"max_tokens": 0,
		"max_completion_tokens": 4096,
		"stop": "done",
		"frequency_penalty": 0,
		"presence_penalty": 0,
		"reasoning_effort": "low",
		"reasoning_budget_tokens": 0,
		"reasoning_enabled": false
	}`))

	transformed := transformChatCompletionRequest(request)
	body, err := io.ReadAll(transformed.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}

	var protoRequest gatewaypb.ChatCompletionRequest
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(body, &protoRequest); err != nil {
		t.Fatalf("transformed body is not a ChatCompletionRequest: %v\n%s", err, body)
	}
	sampling := protoRequest.GetSampling()
	if sampling == nil {
		t.Fatal("sampling is nil")
	}
	if sampling.Temperature == nil || sampling.GetTemperature() != 0 ||
		sampling.TopP == nil || sampling.GetTopP() != 0 ||
		sampling.MaxTokens == nil || sampling.GetMaxTokens() != 0 ||
		sampling.FrequencyPenalty == nil || sampling.GetFrequencyPenalty() != 0 ||
		sampling.PresencePenalty == nil || sampling.GetPresencePenalty() != 0 {
		t.Fatalf("zero-valued sampling presence was lost: %#v", sampling)
	}
	if sampling.GetMaxCompletionTokens() != 4096 ||
		len(sampling.GetStop()) != 1 ||
		sampling.GetStop()[0] != "done" ||
		sampling.GetReasoningEffort() != "low" ||
		sampling.ReasoningBudgetTokens == nil ||
		sampling.GetReasoningBudgetTokens() != 0 ||
		sampling.ReasoningEnabled == nil ||
		sampling.GetReasoningEnabled() {
		t.Fatalf("sampling values were not transformed: %#v", sampling)
	}
}
