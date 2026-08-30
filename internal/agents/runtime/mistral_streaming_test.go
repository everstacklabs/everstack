package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/providers/mistral"
)

func TestMistralReasoningStreamPreservesCompleteTextAndNativeChunks(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`{"id":"chatcmpl-mistral","model":"mistral-small-latest","choices":[{"index":0,"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"private trace"}]}]},"finish_reason":""}]}`,
			`{"id":"chatcmpl-mistral","model":"mistral-small-latest","choices":[{"index":0,"delta":{"content":[{"type":"thinking","thinking":[]},{"type":"text","text":"final "}]},"finish_reason":""}]}`,
			`{"id":"chatcmpl-mistral","model":"mistral-small-latest","choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":""}]}`,
			`{"id":"chatcmpl-mistral","model":"mistral-small-latest","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		}
		for _, chunk := range chunks {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	provider := mistral.NewProvider(mistral.Config{
		APIKey:  "test",
		BaseURL: server.URL,
	})
	engine := NewEngine(nil, nil, nil)
	var streamed strings.Builder

	response, err := engine.ExecuteTurnStream(
		context.Background(),
		provider,
		gw.ChatCompletionRequest{
			Model: "mistral-small-latest",
			Messages: []gw.Message{
				gw.NewMessage(gw.RoleUser, gw.Text("reason carefully")),
			},
			Sampling: gw.SamplingParams{ReasoningEffort: "high"},
		},
		func(delta string) error {
			streamed.WriteString(delta)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("ExecuteTurnStream() error = %v", err)
	}
	if got := streamed.String(); got != "final answer" {
		t.Fatalf("streamed text = %q, want %q", got, "final answer")
	}
	if len(response.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(response.Choices))
	}
	if got := messageText(response.Choices[0].Message); got != "final answer" {
		t.Fatalf("accumulated text = %q, want %q", got, "final answer")
	}
	if got := response.Choices[0].FinishReason; got != "stop" {
		t.Fatalf("finish reason = %q, want %q", got, "stop")
	}

	var nativeTypes []string
	for _, part := range response.Choices[0].Message.Content {
		if part.ProviderJSON != nil {
			var metadata struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(*part.ProviderJSON, &metadata); err != nil {
				t.Fatalf("native provider part is not JSON: %v", err)
			}
			nativeTypes = append(nativeTypes, metadata.Type)
		}
	}
	wantNativeTypes := []string{"thinking", "thinking", "text", "text"}
	if !reflect.DeepEqual(nativeTypes, wantNativeTypes) {
		t.Fatalf("native provider part types = %#v, want %#v", nativeTypes, wantNativeTypes)
	}
}
