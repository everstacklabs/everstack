package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

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
