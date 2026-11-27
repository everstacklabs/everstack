package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/billingmeter"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// strPtr is a tiny helper for the *string fields on gw.ContentPart.
func strPtr(s string) *string { return &s }

// TestToClaude_CacheControl_Ephemeral exercises the prompt-caching
// passthrough wired in for Anthropic. When the caller sets
// req.Metadata["cache_control"] = "ephemeral", we expect three
// breakpoints stamped in the wire body:
//
//  1. The last claudeTool's cache_control.
//  2. The first user message's trailing content block's cache_control.
//  3. The last assistant message's trailing content block's cache_control.
//
// Without the metadata, none of those breakpoints should appear.
func TestToClaude_CacheControl_Ephemeral(t *testing.T) {
	// Build a request that has every section populated: tools, a system
	// prompt (merged-as-user in this client), an assistant turn, and a
	// trailing user turn — mirrors a typical multi-turn chat.
	req := gw.ChatCompletionRequest{
		Model: "claude-3-7-sonnet-latest",
		Tools: []gw.ToolDefinition{
			{
				Type: "function",
				Function: gw.ToolFunctionDef{
					Name:        "search",
					Description: "search the web",
					Parameters:  map[string]interface{}{"type": "object"},
				},
			},
			{
				Type: "function",
				Function: gw.ToolFunctionDef{
					Name:        "calculator",
					Description: "do arithmetic",
					Parameters:  map[string]interface{}{"type": "object"},
				},
			},
		},
		Messages: []gw.Message{
			{
				Role:    gw.RoleSystem,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr("You are a helpful assistant.")}},
			},
			{
				Role:    gw.RoleUser,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr("hi")}},
			},
			{
				Role:    gw.RoleAssistant,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr("Hi! How can I help?")}},
			},
			{
				Role:    gw.RoleUser,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr("what's the weather?")}},
			},
		},
	}

	cases := []struct {
		name           string
		metadata       map[string]interface{}
		wantCacheCount int // expected number of cache_control occurrences in the JSON
	}{
		{
			name:           "no metadata — no breakpoints",
			metadata:       nil,
			wantCacheCount: 0,
		},
		{
			name:           "metadata without cache_control — no breakpoints",
			metadata:       map[string]interface{}{"other_key": "value"},
			wantCacheCount: 0,
		},
		{
			name:           "cache_control wrong value — no breakpoints",
			metadata:       map[string]interface{}{"cache_control": "ignored"},
			wantCacheCount: 0,
		},
		{
			name:           "cache_control ephemeral — three breakpoints",
			metadata:       map[string]interface{}{"cache_control": "ephemeral"},
			wantCacheCount: 3,
		},
	}

	p := &Provider{apiKey: "test-key"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := req
			r.Metadata = tc.metadata
			out, err := p.toClaude(r)
			if err != nil {
				t.Fatalf("toClaude: %v", err)
			}
			body, err := json.Marshal(out)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got := strings.Count(string(body), `"cache_control"`)
			if got != tc.wantCacheCount {
				t.Fatalf("cache_control occurrences: got %d, want %d\nbody=%s", got, tc.wantCacheCount, string(body))
			}
		})
	}
}

// TestToClaude_CacheControl_OnlyTools verifies that when there are no
// assistant messages (e.g. first turn of a conversation), the breakpoints
// land on tools + first user message only — not silently misaligned.
func TestToClaude_CacheControl_OnlyTools(t *testing.T) {
	req := gw.ChatCompletionRequest{
		Model: "claude-3-7-sonnet-latest",
		Tools: []gw.ToolDefinition{
			{
				Type: "function",
				Function: gw.ToolFunctionDef{
					Name:        "search",
					Description: "search the web",
					Parameters:  map[string]interface{}{"type": "object"},
				},
			},
		},
		Messages: []gw.Message{
			{
				Role:    gw.RoleSystem,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr("system prompt")}},
			},
			{
				Role:    gw.RoleUser,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr("first turn")}},
			},
		},
		Metadata: map[string]interface{}{"cache_control": "ephemeral"},
	}

	p := &Provider{apiKey: "test-key"}
	out, err := p.toClaude(req)
	if err != nil {
		t.Fatalf("toClaude: %v", err)
	}

	// Tool should have cache_control.
	if len(out.Tools) == 0 || out.Tools[len(out.Tools)-1].CacheControl == nil {
		t.Fatalf("expected last tool to have cache_control")
	}

	// First user message's last content block should have cache_control.
	if len(out.Messages) == 0 {
		t.Fatalf("expected at least one message")
	}
	if out.Messages[0].Role != "user" {
		t.Fatalf("expected first message role=user, got %s", out.Messages[0].Role)
	}
	last := out.Messages[0].Content[len(out.Messages[0].Content)-1]
	cc, ok := last.(claudeContent)
	if !ok || cc.CacheControl == nil {
		t.Fatalf("expected first user message's last block to be claudeContent with cache_control, got %T %+v", last, last)
	}

	// JSON should contain exactly two cache_control occurrences (no
	// assistant message means no third breakpoint).
	body, _ := json.Marshal(out)
	if got := strings.Count(string(body), `"cache_control"`); got != 2 {
		t.Fatalf("cache_control occurrences: got %d, want 2\nbody=%s", got, string(body))
	}
}

func TestToClaudeReplaysThinkingBeforeToolUse(t *testing.T) {
	raw := json.RawMessage(`{"type":"thinking","thinking":"private reasoning","signature":"signed-state"}`)
	req := gw.ChatCompletionRequest{
		Model: "claude-fable-5-1",
		Messages: []gw.Message{{
			Role: gw.RoleAssistant,
			Content: []gw.ContentPart{{
				Type:         "thinking",
				ProviderJSON: &raw,
			}},
			ToolCalls: []gw.ToolCall{{
				ID:   "toolu_1",
				Type: "function",
				Function: gw.ToolCallFunction{
					Name:      "lookup",
					Arguments: `{"query":"catalog"}`,
				},
			}},
		}},
	}

	out, err := (&Provider{apiKey: "test-key"}).toClaude(req)
	if err != nil {
		t.Fatalf("toClaude() error = %v", err)
	}
	if len(out.Messages) != 1 || len(out.Messages[0].Content) != 2 {
		t.Fatalf("assistant content = %#v, want thinking then tool_use", out.Messages)
	}

	encoded, err := json.Marshal(out.Messages[0].Content)
	if err != nil {
		t.Fatalf("marshal assistant content: %v", err)
	}
	var blocks []map[string]interface{}
	if err := json.Unmarshal(encoded, &blocks); err != nil {
		t.Fatalf("unmarshal assistant content: %v", err)
	}
	if blocks[0]["type"] != "thinking" || blocks[0]["signature"] != "signed-state" {
		t.Fatalf("first block = %#v, want signed thinking block", blocks[0])
	}
	if blocks[1]["type"] != "tool_use" || blocks[1]["id"] != "toolu_1" {
		t.Fatalf("second block = %#v, want tool_use", blocks[1])
	}
}

func TestChatPreservesNativeThinkingBlocks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
            "id":"msg_1",
            "type":"message",
            "role":"assistant",
            "model":"claude-fable-5-1",
            "content":[
              {"type":"thinking","thinking":"private reasoning","signature":"signed-state"},
              {"type":"redacted_thinking","data":"redacted-state"},
              {"type":"tool_use","id":"toolu_1","name":"lookup","input":{"query":"catalog"}}
            ],
            "stop_reason":"tool_use",
            "usage":{"input_tokens":10,"output_tokens":5}
        }`))
	}))
	defer server.Close()

	p := NewProvider(Config{APIKey: "test-key", BaseURL: server.URL})
	p.client = server.Client()
	response, err := p.Chat(context.Background(), gw.ChatCompletionRequest{Model: "claude-fable-5-1"})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	message := response.Choices[0].Message
	if len(message.Content) != 2 {
		t.Fatalf("content length = %d, want two thinking blocks", len(message.Content))
	}
	assertNativeBlock(t, message.Content[0], "thinking", "signature", "signed-state")
	assertNativeBlock(t, message.Content[1], "redacted_thinking", "data", "redacted-state")
	if len(message.ToolCalls) != 1 || message.ToolCalls[0].Function.Arguments != `{"query":"catalog"}` {
		t.Fatalf("tool calls = %#v, want normalized lookup call", message.ToolCalls)
	}
}

func TestChatStreamPreservesThinkingSignatureForReplay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_stream","model":"claude-fable-5-1","usage":{"input_tokens":10}}}`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"private reasoning"}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"signed-stream"}}`,
			`data: {"type":"content_block_stop","index":0}`,
			`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_2","name":"lookup","input":{}}}`,
			`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"catalog\"}"}}`,
			`data: {"type":"content_block_stop","index":1}`,
			`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`,
			`data: {"type":"message_stop"}`,
			"",
		}, "\n")))
	}))
	defer server.Close()

	p := NewProvider(Config{APIKey: "test-key", BaseURL: server.URL})
	p.client = server.Client()
	var nativeParts []gw.ContentPart
	var toolCalls []gw.ToolCall
	err := p.ChatStream(context.Background(), gw.ChatCompletionRequest{Model: "claude-fable-5-1"}, func(chunk gw.ChatResponseChunk) error {
		for _, choice := range chunk.Choices {
			nativeParts = append(nativeParts, choice.Delta.Content...)
			toolCalls = append(toolCalls, choice.Delta.ToolCalls...)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if len(nativeParts) != 1 {
		t.Fatalf("native parts = %#v, want one thinking block", nativeParts)
	}
	assertNativeBlock(t, nativeParts[0], "thinking", "signature", "signed-stream")
	if len(toolCalls) != 1 || toolCalls[0].Function.Arguments != `{"query":"catalog"}` {
		t.Fatalf("tool calls = %#v, want streamed lookup call", toolCalls)
	}

	replay, err := p.toClaude(gw.ChatCompletionRequest{
		Model: "claude-fable-5-1",
		Messages: []gw.Message{{
			Role:      gw.RoleAssistant,
			Content:   nativeParts,
			ToolCalls: toolCalls,
		}},
	})
	if err != nil {
		t.Fatalf("toClaude(replay) error = %v", err)
	}
	encoded, _ := json.Marshal(replay.Messages[0].Content[0])
	var replayed map[string]interface{}
	if err := json.Unmarshal(encoded, &replayed); err != nil {
		t.Fatalf("unmarshal replayed thinking: %v", err)
	}
	if replayed["thinking"] != "private reasoning" || replayed["signature"] != "signed-stream" {
		t.Fatalf("replayed thinking = %#v", replayed)
	}
}

func assertNativeBlock(t *testing.T, part gw.ContentPart, wantType, field, wantValue string) {
	t.Helper()
	if part.Type != wantType || part.ProviderJSON == nil {
		t.Fatalf("part = %#v, want native %s block", part, wantType)
	}
	var block map[string]interface{}
	if err := json.Unmarshal(*part.ProviderJSON, &block); err != nil {
		t.Fatalf("unmarshal provider JSON: %v", err)
	}
	if block["type"] != wantType || block[field] != wantValue {
		t.Fatalf("native block = %#v, want type=%s %s=%s", block, wantType, field, wantValue)
	}
}

// Anthropic streams thinking as deltas, but the provider emits the block only
// when it ends. While it is buffered, the metering seam's PendingOutput holds
// its size, so a stream that breaks off mid-thinking can still be estimated.
func TestChatStreamReportsBufferedThinkingAsPending(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_stream","model":"claude-fable-5-1","usage":{"input_tokens":10}}}`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"` + strings.Repeat("a", 300) + `"}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"` + strings.Repeat("b", 100) + `"}}`,
			"", "",
		}, "\n")))
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)

	p := NewProvider(Config{APIKey: "test-key", BaseURL: server.URL})
	p.client = server.Client()
	pending := &billingmeter.PendingOutput{}
	ctx, cancel := context.WithCancel(billingmeter.WithPendingOutput(context.Background(), pending))
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- p.ChatStream(ctx, gw.ChatCompletionRequest{Model: "claude-fable-5-1"}, func(gw.ChatResponseChunk) error { return nil })
	}()
	deadline := time.Now().Add(5 * time.Second)
	for pending.ReasoningChars() != 400 {
		if time.Now().After(deadline) {
			t.Fatalf("pending reasoning chars = %d, want 400", pending.ReasoningChars())
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel() // the client goes away mid-thinking
	if err := <-done; err == nil {
		t.Fatal("ChatStream must report the aborted stream")
	}
	if got := pending.ReasoningChars(); got != 400 {
		t.Fatalf("pending after abort = %d, want 400", got)
	}
}

// Once the block is emitted, nothing is pending.
func TestChatStreamClearsPendingThinkingOnceEmitted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_stream","model":"claude-fable-5-1","usage":{"input_tokens":10}}}`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"private reasoning"}}`,
			`data: {"type":"content_block_stop","index":0}`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
			`data: {"type":"message_stop"}`,
			"",
		}, "\n")))
	}))
	defer server.Close()

	p := NewProvider(Config{APIKey: "test-key", BaseURL: server.URL})
	p.client = server.Client()
	pending := &billingmeter.PendingOutput{}
	sawPending := 0
	err := p.ChatStream(billingmeter.WithPendingOutput(context.Background(), pending), gw.ChatCompletionRequest{Model: "claude-fable-5-1"},
		func(gw.ChatResponseChunk) error {
			if n := pending.ReasoningChars(); n > sawPending {
				sawPending = n
			}
			return nil
		})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if got := pending.ReasoningChars(); got != 0 {
		t.Fatalf("pending after the block was emitted = %d, want 0", got)
	}
	if sawPending != 0 {
		t.Fatalf("pending while the emitted block was delivered = %d, want 0 (no double count)", sawPending)
	}
}

// message_start already carries the exact prompt usage; the provider hands
// it to the metering seam before any output, so an abort is priced on it.
func TestChatStreamReportsPromptUsageFromMessageStart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_stream","model":"claude-fable-5-1","usage":{"input_tokens":5000,"cache_read_input_tokens":45000,"cache_creation_input_tokens":200}}}`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"partial"}}`,
			"",
		}, "\n")))
	}))
	defer server.Close()

	p := NewProvider(Config{APIKey: "test-key", BaseURL: server.URL})
	p.client = server.Client()
	pending := &billingmeter.PendingOutput{}
	err := p.ChatStream(billingmeter.WithPendingOutput(context.Background(), pending), gw.ChatCompletionRequest{Model: "claude-fable-5-1"},
		func(gw.ChatResponseChunk) error { return nil })
	prompt, cacheRead, cacheWrite, ok := pending.PromptUsage()
	if !ok || prompt != 50200 || cacheRead != 45000 || cacheWrite != 200 {
		t.Fatalf("prompt usage = %d/%d/%d ok=%v, want 50200 inclusive, 45000 read, 200 write", prompt, cacheRead, cacheWrite, ok)
	}
	// The upstream cut off without message_delta or message_stop: that is
	// an aborted stream, not a completed one, so its buffered thinking is
	// counted.
	if err == nil {
		t.Fatal("a stream cut off before its final events must be an error")
	}
	if got := pending.ReasoningChars(); got != len("partial") {
		t.Fatalf("pending reasoning = %d", got)
	}
}
