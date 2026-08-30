package openai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

func TestIsResponsesModel(t *testing.T) {
	p := &Provider{cfg: Config{
		ResponsesModels: []string{"custom-responses-model"},
	}}

	tests := []struct {
		model string
		want  bool
	}{
		{"gpt-5.2-codex", true},
		{"gpt-5-codex", true},
		{"gpt-5.1-codex-mini", true},
		{"gpt-5.3-codex-spark", true},
		{"custom-responses-model", true},
		{"Custom-Responses-Model", true}, // case-insensitive
		{"gpt-4o", false},
		{"gpt-5", false},
		{"o1-preview", false},
		{"babbage-002", false}, // not a responses model
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := p.isResponsesModel(tt.model)
			if got != tt.want {
				t.Errorf("isResponsesModel(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

func TestMessagesToResponsesInput(t *testing.T) {
	sysText := "You are a helpful assistant."
	userText := "Write hello world in Python"
	assistantText := "print('hello world')"

	messages := []gw.Message{
		{Role: gw.RoleSystem, Content: []gw.ContentPart{gw.Text(sysText)}},
		{Role: gw.RoleUser, Content: []gw.ContentPart{gw.Text(userText)}},
		{Role: gw.RoleAssistant, Content: []gw.ContentPart{gw.Text(assistantText)}},
	}

	instructions, input := messagesToResponsesInput(messages)

	// System message should become instructions
	if instructions != sysText {
		t.Errorf("instructions = %q, want %q", instructions, sysText)
	}

	// Should have 2 input items (user + assistant, not system)
	if len(input) != 2 {
		t.Fatalf("len(input) = %d, want 2", len(input))
	}

	if input[0].Role != "user" {
		t.Errorf("input[0].Role = %q, want user", input[0].Role)
	}
	// Single text part should be a string
	if s, ok := input[0].Content.(string); !ok || s != userText {
		t.Errorf("input[0].Content = %v, want %q", input[0].Content, userText)
	}

	if input[1].Role != "assistant" {
		t.Errorf("input[1].Role = %q, want assistant", input[1].Role)
	}
}

func TestMessagesToResponsesInput_EmptyAssistantContentNeverNull(t *testing.T) {
	messages := []gw.Message{
		{Role: gw.RoleUser, Content: []gw.ContentPart{gw.Text("run tool")}},
		{
			Role:      gw.RoleAssistant,
			ToolCalls: []gw.ToolCall{{ID: "call_1", Type: "function", Function: gw.ToolCallFunction{Name: "list_models", Arguments: `{}`}}},
			Content:   nil,
		},
	}

	_, input := messagesToResponsesInput(messages)
	// Expect 3 items: user message + assistant message (empty content) + function_call
	if len(input) != 3 {
		t.Fatalf("len(input) = %d, want 3", len(input))
	}
	// The assistant message item (index 1) must have non-nil content even when
	// the original message had no text (only tool calls).
	if input[1].Content == nil {
		t.Fatalf("input[1].Content must not be nil")
	}
	if s, ok := input[1].Content.(string); !ok || s != "" {
		t.Fatalf("input[1].Content = %#v, want empty string", input[1].Content)
	}
	// The function_call item (index 2) should have the tool call details
	if input[2].Type != "function_call" || input[2].Name != "list_models" {
		t.Fatalf("input[2] = %#v, want function_call for list_models", input[2])
	}
}

func TestResponsesToGatewayResponse(t *testing.T) {
	p := &Provider{}
	oa := oaResponsesResponse{
		ID:        "resp-123",
		CreatedAt: 1700000000,
		Model:     "gpt-5.2-codex",
		Status:    "completed",
		Output: []oaResponsesOutputItem{
			{
				ID:   "msg-1",
				Type: "message",
				Role: "assistant",
				Content: []oaResponsesOutputContent{
					{Type: "output_text", Text: "print('hello')"},
				},
			},
		},
		OutputText: "print('hello')",
		Usage: oaResponsesUsage{
			InputTokens:  10,
			OutputTokens: 5,
			TotalTokens:  15,
		},
	}

	resp := p.responsesToGatewayResponse(oa)

	if resp.ID != "resp-123" {
		t.Errorf("ID = %q, want resp-123", resp.ID)
	}
	if resp.Model != "gpt-5.2-codex" {
		t.Errorf("Model = %q, want gpt-5.2-codex", resp.Model)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("len(Choices) = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want stop", resp.Choices[0].FinishReason)
	}
	if len(resp.Choices[0].Message.Content) != 1 {
		t.Fatalf("len(Content) = %d, want 1", len(resp.Choices[0].Message.Content))
	}
	if *resp.Choices[0].Message.Content[0].Text != "print('hello')" {
		t.Errorf("Content text = %q, want print('hello')", *resp.Choices[0].Message.Content[0].Text)
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 5 || resp.Usage.TotalTokens != 15 {
		t.Errorf("Usage = %+v, want 10/5/15", resp.Usage)
	}
}

func TestResponsesToGatewayResponseWithToolCalls(t *testing.T) {
	p := &Provider{}
	oa := oaResponsesResponse{
		ID:        "resp-456",
		CreatedAt: 1700000000,
		Model:     "gpt-5.2-codex",
		Status:    "completed",
		Output: []oaResponsesOutputItem{
			{
				ID:        "fc-1",
				Type:      "function_call",
				Name:      "get_weather",
				CallID:    "call-abc",
				Arguments: `{"location":"Paris"}`,
			},
		},
		Usage: oaResponsesUsage{
			InputTokens:  20,
			OutputTokens: 10,
			TotalTokens:  30,
		},
	}

	resp := p.responsesToGatewayResponse(oa)

	if len(resp.Choices) != 1 {
		t.Fatalf("len(Choices) = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].FinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q, want tool_calls", resp.Choices[0].FinishReason)
	}
	if len(resp.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(resp.Choices[0].Message.ToolCalls))
	}
	tc := resp.Choices[0].Message.ToolCalls[0]
	if tc.ID != "call-abc" || tc.Function.Name != "get_weather" || tc.Function.Arguments != `{"location":"Paris"}` {
		t.Errorf("ToolCall = %+v", tc)
	}
}

func TestChatViaResponsesEndpoint(t *testing.T) {
	// Start a test server that mimics the OpenAI /v1/responses endpoint
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("expected path /responses, got %s", r.URL.Path)
			http.Error(w, "wrong path", 404)
			return
		}

		var reqBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		if reqBody["model"] != "gpt-5.2-codex" {
			t.Errorf("expected model gpt-5.2-codex, got %v", reqBody["model"])
		}
		if reqBody["instructions"] == nil || reqBody["instructions"] == "" {
			// instructions can be empty if no system message
		}

		resp := oaResponsesResponse{
			ID:        "resp-test",
			CreatedAt: 1700000000,
			Model:     "gpt-5.2-codex",
			Status:    "completed",
			Output: []oaResponsesOutputItem{
				{
					ID:   "msg-1",
					Type: "message",
					Role: "assistant",
					Content: []oaResponsesOutputContent{
						{Type: "output_text", Text: "completed text"},
					},
				},
			},
			OutputText: "completed text",
			Usage: oaResponsesUsage{
				InputTokens:  10,
				OutputTokens: 5,
				TotalTokens:  15,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := NewProvider(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
	})

	req := gw.ChatCompletionRequest{
		Model: "gpt-5.2-codex",
		Messages: []gw.Message{
			{Role: gw.RoleUser, Content: []gw.ContentPart{gw.Text("hello")}},
		},
	}

	resp, err := p.Chat(t.Context(), req)
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	if resp.ID != "resp-test" {
		t.Errorf("resp.ID = %q, want resp-test", resp.ID)
	}
	if len(resp.Choices) != 1 || *resp.Choices[0].Message.Content[0].Text != "completed text" {
		t.Errorf("unexpected response content")
	}
}

func TestChatStreamViaResponsesEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("expected path /responses, got %s", r.URL.Path)
			http.Error(w, "wrong path", 404)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)

		// Send response.created
		fmt.Fprintf(w, "event: response.created\ndata: {\"id\":\"resp-stream\",\"model\":\"gpt-5.2-codex\"}\n\n")
		flusher.Flush()

		// Send text deltas
		fmt.Fprintf(w, "event: response.output_text.delta\ndata: {\"delta\":\"Hello \"}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "event: response.output_text.delta\ndata: {\"delta\":\"world!\"}\n\n")
		flusher.Flush()

		// Send completion
		completedJSON := `{"id":"resp-stream","model":"gpt-5.2-codex","status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}`
		fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", completedJSON)
		flusher.Flush()
	}))
	defer srv.Close()

	p := NewProvider(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
	})

	req := gw.ChatCompletionRequest{
		Model: "gpt-5.2-codex",
		Messages: []gw.Message{
			{Role: gw.RoleUser, Content: []gw.ContentPart{gw.Text("hello")}},
		},
	}

	var chunks []gw.ChatResponseChunk
	err := p.ChatStream(t.Context(), req, func(chunk gw.ChatResponseChunk) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream() error: %v", err)
	}

	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks (2 deltas + completion), got %d", len(chunks))
	}

	// Verify text deltas
	var text strings.Builder
	for _, c := range chunks {
		for _, choice := range c.Choices {
			for _, part := range choice.Delta.Content {
				if part.Text != nil {
					text.WriteString(*part.Text)
				}
			}
		}
	}
	if text.String() != "Hello world!" {
		t.Errorf("accumulated text = %q, want \"Hello world!\"", text.String())
	}

	// Verify final chunk has usage
	lastChunk := chunks[len(chunks)-1]
	if lastChunk.Usage == nil {
		t.Fatal("expected usage in final chunk")
	}
	if lastChunk.Usage.PromptTokens != 5 || lastChunk.Usage.CompletionTokens != 3 {
		t.Errorf("Usage = %+v, want 5/3", lastChunk.Usage)
	}
}

func TestChatStreamViaResponsesFunctionCallFromCompleted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("expected path /responses, got %s", r.URL.Path)
			http.Error(w, "wrong path", 404)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)

		fmt.Fprintf(w, "event: response.created\ndata: {\"id\":\"resp-tool\",\"model\":\"gpt-5.2-codex\"}\n\n")
		flusher.Flush()

		completedJSON := `{"id":"resp-tool","model":"gpt-5.2-codex","status":"completed","output":[{"id":"fc_1","type":"function_call","call_id":"call_1","name":"ask_user","arguments":"{\"question\":\"clarify scope\"}"}],"usage":{"input_tokens":7,"output_tokens":2,"total_tokens":9}}`
		fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", completedJSON)
		flusher.Flush()
	}))
	defer srv.Close()

	p := NewProvider(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
	})

	req := gw.ChatCompletionRequest{
		Model: "gpt-5.2-codex",
		Messages: []gw.Message{
			{Role: gw.RoleUser, Content: []gw.ContentPart{gw.Text("delegate this")}},
		},
	}

	var chunks []gw.ChatResponseChunk
	err := p.ChatStream(t.Context(), req, func(chunk gw.ChatResponseChunk) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream() error: %v", err)
	}

	foundToolCall := false
	finalFinishReason := ""
	for _, chunk := range chunks {
		for _, choice := range chunk.Choices {
			if choice.FinishReason != "" {
				finalFinishReason = choice.FinishReason
			}
			for _, tc := range choice.Delta.ToolCalls {
				if tc.Function.Name == "ask_user" && strings.Contains(tc.Function.Arguments, "clarify scope") {
					foundToolCall = true
				}
			}
		}
	}

	if !foundToolCall {
		t.Fatalf("expected a streamed tool call assembled from response.completed")
	}
	if finalFinishReason != "tool_calls" {
		t.Fatalf("finish reason = %q, want %q", finalFinishReason, "tool_calls")
	}
}

func TestChatStreamViaResponsesMultilineDataEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("expected path /responses, got %s", r.URL.Path)
			http.Error(w, "wrong path", 404)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)

		fmt.Fprintf(w, "event: response.created\ndata: {\"id\":\"resp-multiline\",\"model\":\"gpt-5.2-codex\"}\n\n")
		flusher.Flush()

		fmt.Fprintf(w, "event: response.completed\n")
		fmt.Fprintf(w, "data: {\"id\":\"resp-multiline\",\"model\":\"gpt-5.2-codex\",\"status\":\"completed\",\n")
		fmt.Fprintf(w, "data: \"output\":[{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"Hello multiline\"}]}],\"usage\":{\"input_tokens\":3,\"output_tokens\":4,\"total_tokens\":7}}\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	p := NewProvider(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
	})

	req := gw.ChatCompletionRequest{
		Model: "gpt-5.2-codex",
		Messages: []gw.Message{
			{Role: gw.RoleUser, Content: []gw.ContentPart{gw.Text("hello")}},
		},
	}

	var chunks []gw.ChatResponseChunk
	err := p.ChatStream(t.Context(), req, func(chunk gw.ChatResponseChunk) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream() error: %v", err)
	}

	var text strings.Builder
	for _, c := range chunks {
		for _, choice := range c.Choices {
			for _, part := range choice.Delta.Content {
				if part.Text != nil {
					text.WriteString(*part.Text)
				}
			}
		}
	}
	if text.String() != "Hello multiline" {
		t.Fatalf("accumulated text = %q, want %q", text.String(), "Hello multiline")
	}

	lastChunk := chunks[len(chunks)-1]
	if lastChunk.Usage == nil {
		t.Fatalf("expected usage in final chunk")
	}
	if lastChunk.Usage.TotalTokens != 7 {
		t.Fatalf("total tokens = %d, want 7", lastChunk.Usage.TotalTokens)
	}
}

func TestChatStreamViaResponsesMergesItemAndCallIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("expected path /responses, got %s", r.URL.Path)
			http.Error(w, "wrong path", 404)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)

		fmt.Fprintf(w, "event: response.created\ndata: {\"id\":\"resp-alias\",\"model\":\"gpt-5.2-codex\"}\n\n")
		flusher.Flush()

		fmt.Fprintf(w, "event: response.function_call_arguments.delta\ndata: {\"item_id\":\"fc_item_1\",\"delta\":\"{\\\"question\\\":\\\"clarify scope\\\"}\"}\n\n")
		flusher.Flush()

		fmt.Fprintf(w, "event: response.output_item.added\ndata: {\"item\":{\"id\":\"fc_item_1\",\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"ask_user\"}}\n\n")
		flusher.Flush()

		completedJSON := `{"id":"resp-alias","model":"gpt-5.2-codex","status":"completed","output":[],"usage":{"input_tokens":4,"output_tokens":1,"total_tokens":5}}`
		fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", completedJSON)
		flusher.Flush()
	}))
	defer srv.Close()

	p := NewProvider(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
	})

	req := gw.ChatCompletionRequest{
		Model: "gpt-5.2-codex",
		Messages: []gw.Message{
			{Role: gw.RoleUser, Content: []gw.ContentPart{gw.Text("need clarification")}},
		},
	}

	var chunks []gw.ChatResponseChunk
	err := p.ChatStream(t.Context(), req, func(chunk gw.ChatResponseChunk) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream() error: %v", err)
	}

	var found []gw.ToolCall
	for _, chunk := range chunks {
		for _, choice := range chunk.Choices {
			if len(choice.Delta.ToolCalls) > 0 {
				found = append(found, choice.Delta.ToolCalls...)
			}
		}
	}

	if len(found) != 1 {
		t.Fatalf("expected exactly 1 merged tool call, got %d", len(found))
	}
	if found[0].ID != "call_1" {
		t.Fatalf("tool call id = %q, want %q", found[0].ID, "call_1")
	}
	if found[0].Function.Name != "ask_user" {
		t.Fatalf("tool call name = %q, want %q", found[0].Function.Name, "ask_user")
	}
	if !strings.Contains(found[0].Function.Arguments, "clarify scope") {
		t.Fatalf("tool call args = %q, want to contain clarify scope", found[0].Function.Arguments)
	}
}

func TestMessagesToResponsesInputSkipsMalformedFunctionCalls(t *testing.T) {
	messages := []gw.Message{
		{Role: gw.RoleUser, Content: []gw.ContentPart{gw.Text("run a tool")}},
		{
			Role: gw.RoleAssistant,
			ToolCalls: []gw.ToolCall{
				{ID: "call_bad", Type: "function", Function: gw.ToolCallFunction{Name: "", Arguments: `{"x":1}`}},
				{ID: "call_good", Type: "function", Function: gw.ToolCallFunction{Name: "ask_user", Arguments: `{"question":"hi"}`}},
			},
		},
		{Role: gw.RoleTool, ToolCallID: "call_bad", Content: []gw.ContentPart{gw.Text("bad output")}},
		{Role: gw.RoleTool, ToolCallID: "call_good", Content: []gw.ContentPart{gw.Text("good output")}},
	}

	_, input := messagesToResponsesInput(messages)

	for _, item := range input {
		if item.Type == "function_call" && item.Name == "" {
			t.Fatalf("found malformed function_call with empty name: %#v", item)
		}
		if item.Type == "function_call_output" && item.CallID == "call_bad" {
			t.Fatalf("found orphaned function_call_output for skipped call_bad: %#v", item)
		}
	}

	foundGoodCall := false
	foundGoodOutput := false
	for _, item := range input {
		if item.Type == "function_call" && item.CallID == "call_good" && item.Name == "ask_user" {
			foundGoodCall = true
		}
		if item.Type == "function_call_output" && item.CallID == "call_good" {
			foundGoodOutput = true
		}
	}
	if !foundGoodCall || !foundGoodOutput {
		t.Fatalf("expected valid call/output pair for call_good, got input=%#v", input)
	}
}

func TestChatNonResponsesModel(t *testing.T) {
	// Verify that non-codex models still use /chat/completions
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("expected path /chat/completions, got %s", r.URL.Path)
			http.Error(w, "wrong path", 404)
			return
		}

		resp := map[string]interface{}{
			"id":      "chatcmpl-test",
			"created": 1700000000,
			"model":   "gpt-4o",
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": "hello back",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]interface{}{
				"prompt_tokens":     5,
				"completion_tokens": 3,
				"total_tokens":      8,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := NewProvider(Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
	})

	req := gw.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []gw.Message{
			{Role: gw.RoleUser, Content: []gw.ContentPart{gw.Text("hello")}},
		},
	}

	resp, err := p.Chat(t.Context(), req)
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	if resp.ID != "chatcmpl-test" {
		t.Errorf("resp.ID = %q, want chatcmpl-test", resp.ID)
	}
}
