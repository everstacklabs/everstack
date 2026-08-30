package runtime

import (
	"encoding/json"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

func assistantMsg(text string, calls ...gw.ToolCall) gw.Message {
	msg := gw.Message{Role: gw.RoleAssistant, ToolCalls: calls}
	if text != "" {
		msg.Content = []gw.ContentPart{{Type: "text", Text: strPtr(text)}}
	}
	return msg
}

func askUserCall(id, question string) gw.ToolCall {
	return gw.ToolCall{
		ID:       id,
		Type:     "function",
		Function: gw.ToolCallFunction{Name: "ask_user", Arguments: `{"question":"` + question + `"}`},
	}
}

func toolCall(id, name, args string) gw.ToolCall {
	return gw.ToolCall{
		ID:       id,
		Type:     "function",
		Function: gw.ToolCallFunction{Name: name, Arguments: args},
	}
}

func TestBuildTurnTimeline_InterleavesAndScopesToTurn(t *testing.T) {
	state := &LoopState{
		Messages: []gw.Message{
			// Prior turn — must be excluded by TurnStartIndex scoping.
			{Role: gw.RoleUser, Content: []gw.ContentPart{{Type: "text", Text: strPtr("prior question")}}},
			assistantMsg("prior answer"),
			// Current turn begins here (index 2).
			{Role: gw.RoleUser, Content: []gw.ContentPart{{Type: "text", Text: strPtr("whats the weather")}}},
			assistantMsg("Let me ask.", askUserCall("a1", "Which city?")),
			{Role: gw.RoleTool, ToolCallID: "a1", Content: []gw.ContentPart{{Type: "text", Text: strPtr("ignored")}}},
			assistantMsg("Now searching.", toolCall("t1", "web_search", `{"q":"weather"}`)),
			{Role: gw.RoleTool, ToolCallID: "t1", Content: []gw.ContentPart{{Type: "text", Text: strPtr("ignored")}}},
			assistantMsg("It is sunny."),
		},
		TurnStartIndex: 2,
		ToolResults: map[string]ToolResultMeta{
			"a1": {Result: WrapUserAnswer("Dublin"), Success: true},
			"t1": {Result: "sunny 20C", Success: true, DurationMs: 100},
		},
	}

	raw := buildTurnTimeline(state)
	if raw == "" {
		t.Fatal("expected a non-empty timeline")
	}
	var items []timelineItem
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		t.Fatalf("timeline is not valid JSON: %v", err)
	}

	if len(items) != 5 {
		t.Fatalf("expected 5 timeline items, got %d: %s", len(items), raw)
	}

	// Order and content must match the true execution sequence.
	want := []struct {
		typ     string
		content string
	}{
		{"text", "Let me ask."},
		{"hitl", ""},
		{"text", "Now searching."},
		{"tool", ""},
		{"text", "It is sunny."},
	}
	for i, w := range want {
		if items[i].T != w.typ {
			t.Errorf("item %d: type = %q, want %q", i, items[i].T, w.typ)
		}
		if w.content != "" && items[i].Content != w.content {
			t.Errorf("item %d: content = %q, want %q", i, items[i].Content, w.content)
		}
	}

	// The HITL answer must be the user's clean words, not the steering wrapper.
	hitl := items[1]
	if hitl.Question != "Which city?" {
		t.Errorf("hitl question = %q, want %q", hitl.Question, "Which city?")
	}
	if hitl.Response != "Dublin" {
		t.Errorf("hitl response = %q, want %q (wrapper must be stripped)", hitl.Response, "Dublin")
	}
	if hitl.Cancelled {
		t.Error("hitl should not be marked cancelled")
	}

	// The tool item carries name/result/duration.
	tool := items[3]
	if tool.Name != "web_search" || tool.Result != "sunny 20C" || tool.DurationMs != 100 {
		t.Errorf("tool item = %+v, want web_search/sunny 20C/100", tool)
	}
	if tool.Success == nil || !*tool.Success {
		t.Error("tool success should be true")
	}
}

func TestBuildTurnTimeline_EmptyWhenNoTurnContent(t *testing.T) {
	state := &LoopState{
		Messages:       []gw.Message{assistantMsg("prior")},
		TurnStartIndex: 1, // points at end — current turn has no messages yet
	}
	if got := buildTurnTimeline(state); got != "" {
		t.Errorf("expected empty timeline, got %q", got)
	}
}

func TestBuildTurnTimeline_SkipsToolCallsWithoutResults(t *testing.T) {
	state := &LoopState{
		Messages: []gw.Message{
			assistantMsg("working", toolCall("t1", "web_search", "{}")),
		},
		TurnStartIndex: 0,
		ToolResults:    map[string]ToolResultMeta{}, // no result recorded
	}
	var items []timelineItem
	_ = json.Unmarshal([]byte(buildTurnTimeline(state)), &items)
	for _, it := range items {
		if it.T == "tool" {
			t.Errorf("tool call without a result should be skipped, got %+v", it)
		}
	}
}

func TestDisplayUserAnswer(t *testing.T) {
	cases := map[string]string{
		WrapUserAnswer("Dublin"):    "Dublin",
		WrapUserAnswer("New York"):  "New York",
		"Dublin":                    "Dublin", // already clean
		"User answered: Dublin":     "Dublin", // prefix only
		"":                          "",
		"  spaced answer  ":         "spaced answer",
	}
	for in, want := range cases {
		if got := DisplayUserAnswer(in); got != want {
			t.Errorf("DisplayUserAnswer(%q) = %q, want %q", in, got, want)
		}
	}
}
