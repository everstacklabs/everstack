package everstack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	c := NewClient("pk_test")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.Chat == nil {
		t.Error("Chat resource is nil")
	}
	if c.Embeddings == nil {
		t.Error("Embeddings resource is nil")
	}
	if c.Models == nil {
		t.Error("Models resource is nil")
	}
	if c.Audio == nil {
		t.Error("Audio resource is nil")
	}
	if c.Images == nil {
		t.Error("Images resource is nil")
	}
	if c.Moderations == nil {
		t.Error("Moderations resource is nil")
	}
	if c.Rerank == nil {
		t.Error("Rerank resource is nil")
	}
	if c.Responses == nil {
		t.Error("Responses resource is nil")
	}
	if c.Agents == nil {
		t.Error("Agents resource is nil")
	}
	if c.Datasets == nil {
		t.Error("Datasets resource is nil")
	}
	if c.Evaluations == nil {
		t.Error("Evaluations resource is nil")
	}
	if c.Observability == nil {
		t.Error("Observability resource is nil")
	}
}

func TestClientOptions(t *testing.T) {
	c := NewClient("pk_test",
		WithBaseURL("http://localhost:9090"),
		WithProvider("@openai"),
		WithOrgID("org_123"),
		WithUserID("user_456"),
		WithTimeout(30*time.Second),
	)
	if c.transport.baseURL != "http://localhost:9090" {
		t.Errorf("expected base URL http://localhost:9090, got %s", c.transport.baseURL)
	}
	if c.transport.provider != "@openai" {
		t.Errorf("expected provider @openai, got %s", c.transport.provider)
	}
	if c.transport.orgID != "org_123" {
		t.Errorf("expected org_id org_123, got %s", c.transport.orgID)
	}
	if c.transport.userID != "user_456" {
		t.Errorf("expected user_id user_456, got %s", c.transport.userID)
	}
}

func TestChatCompletionCreate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if r.Header.Get("x-evs-api-key") != "pk_test" {
			t.Errorf("missing api key header")
		}

		content := "Hello!"
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:    "chatcmpl-1",
			Model: "@openai/gpt-4o",
			Choices: []Choice{
				{Index: 0, Message: ChoiceMessage{Role: "assistant", Content: &content}},
			},
			Usage: Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		})
	}))
	defer srv.Close()

	c := NewClient("pk_test", WithBaseURL(srv.URL))
	resp, err := c.Chat.Completions.Create(context.Background(), &ChatCompletionParams{
		Model:    "@openai/gpt-4o",
		Messages: []Message{{Role: "user", Content: "Hello!"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ID != "chatcmpl-1" {
		t.Errorf("expected ID chatcmpl-1, got %s", resp.ID)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content == nil || *resp.Choices[0].Message.Content != "Hello!" {
		t.Error("unexpected content")
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("expected 15 total tokens, got %d", resp.Usage.TotalTokens)
	}
}

func TestEmbeddingsCreate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(EmbeddingsResponse{
			Object: "list",
			Data:   []EmbeddingData{{Object: "embedding", Embedding: []float64{0.1, 0.2}, Index: 0}},
			Model:  "@openai/text-embedding-3-small",
			Usage:  EmbeddingsUsage{PromptTokens: 5, TotalTokens: 5},
		})
	}))
	defer srv.Close()

	c := NewClient("pk_test", WithBaseURL(srv.URL))
	resp, err := c.Embeddings.Create(context.Background(), &EmbeddingsParams{
		Model: "@openai/text-embedding-3-small",
		Input: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 embedding, got %d", len(resp.Data))
	}
	if len(resp.Data[0].Embedding) != 2 {
		t.Errorf("expected 2 dims, got %d", len(resp.Data[0].Embedding))
	}
}

func TestModelsList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		json.NewEncoder(w).Encode(ModelsListResponse{
			Object: "list",
			Data: []ModelInfo{
				{ID: "@openai/gpt-4o", Object: "model"},
				{ID: "@anthropic/claude-3-5-sonnet-20241022", Object: "model"},
			},
		})
	}))
	defer srv.Close()

	c := NewClient("pk_test", WithBaseURL(srv.URL))
	resp, err := c.Models.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 models, got %d", len(resp.Data))
	}
}

func TestStreamingChatCompletion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected flusher")
		}

		chunks := []string{
			`{"id":"c1","choices":[{"index":0,"delta":{"content":"Hello"}}]}`,
			`{"id":"c1","choices":[{"index":0,"delta":{"content":" world"}}]}`,
		}
		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	c := NewClient("pk_test", WithBaseURL(srv.URL))
	stream, err := c.Chat.Completions.CreateStream(context.Background(), &ChatCompletionParams{
		Model:    "@openai/gpt-4o",
		Messages: []Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	var chunks []ChatCompletionChunk
	for stream.Next() {
		chunks = append(chunks, stream.Current())
	}
	if err := stream.Err(); err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if chunks[0].Choices[0].Delta.Content == nil || *chunks[0].Choices[0].Delta.Content != "Hello" {
		t.Error("unexpected first chunk content")
	}
}

func TestAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]string{"message": "Invalid API key"})
	}))
	defer srv.Close()

	c := NewClient("bad_key", WithBaseURL(srv.URL))
	_, err := c.Models.List(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}

	authErr, ok := err.(*AuthenticationError)
	if !ok {
		t.Fatalf("expected AuthenticationError, got %T: %v", err, err)
	}
	if authErr.StatusCode != 401 {
		t.Errorf("expected 401, got %d", authErr.StatusCode)
	}
}

func TestNotFoundError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]string{"message": "Not found"})
	}))
	defer srv.Close()

	c := NewClient("pk_test", WithBaseURL(srv.URL))
	_, err := c.Responses.Get(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}

	notFound, ok := err.(*NotFoundError)
	if !ok {
		t.Fatalf("expected NotFoundError, got %T", err)
	}
	if notFound.StatusCode != 404 {
		t.Errorf("expected 404, got %d", notFound.StatusCode)
	}
}
