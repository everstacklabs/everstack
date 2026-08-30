package runtime

import (
	"encoding/json"
	"strings"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// timelineItem is one entry in a turn's ordered timeline. The "t" discriminator
// selects which fields are populated:
//   - "text": Content (an assistant narration / response segment)
//   - "tool": ID, Name, Args, Result, Success, DurationMs, SandboxParentDurationMs
//   - "hitl": ID, Question, Response, Cancelled (an ask_user exchange)
//
// The shape mirrors the frontend's live TimelineSegment so a reloaded turn
// renders identically to the streamed one.
type timelineItem struct {
	T                       string `json:"t"`
	Content                 string `json:"content,omitempty"`
	ID                      string `json:"id,omitempty"`
	Name                    string `json:"name,omitempty"`
	Args                    string `json:"args,omitempty"`
	Result                  string `json:"result,omitempty"`
	Success                 *bool  `json:"success,omitempty"`
	DurationMs              int64  `json:"duration_ms,omitempty"`
	SandboxParentDurationMs int64  `json:"sandbox_parent_duration_ms,omitempty"`
	Question                string `json:"question,omitempty"`
	Response                string `json:"response,omitempty"`
	Cancelled               bool   `json:"cancelled,omitempty"`
}

// buildTurnTimeline reconstructs the ordered sequence of assistant text, tool
// calls, and HITL exchanges for the current turn from the in-memory
// conversation (state.Messages[TurnStartIndex:]). Tool calls without a recorded
// result are skipped, mirroring the flat tool_calls builder. Returns "" when
// there is nothing to persist, so the column stays NULL and the UI falls back
// to the flat user_input / assistant_output / tool_calls fields.
func buildTurnTimeline(state *LoopState) string {
	start := state.TurnStartIndex
	if start < 0 {
		start = 0
	}
	if start > len(state.Messages) {
		return ""
	}

	var items []timelineItem
	for _, msg := range state.Messages[start:] {
		// Only assistant messages carry the model's text and tool calls. Tool
		// result messages are folded into their tool item via ToolResults; the
		// turn's user input is persisted separately in user_input.
		if msg.Role != gw.RoleAssistant {
			continue
		}
		if text := strings.TrimSpace(messageText(msg)); text != "" {
			items = append(items, timelineItem{T: "text", Content: text})
		}
		for _, tc := range msg.ToolCalls {
			meta, ok := state.ToolResults[tc.ID]
			if !ok {
				continue
			}
			if tc.Function.Name == "ask_user" {
				items = append(items, timelineItem{
					T:         "hitl",
					ID:        tc.ID,
					Question:  extractAskUserQuestion(tc.Function.Arguments),
					Response:  DisplayUserAnswer(meta.Result),
					Cancelled: !meta.Success,
				})
				continue
			}
			success := meta.Success
			items = append(items, timelineItem{
				T:                       "tool",
				ID:                      tc.ID,
				Name:                    tc.Function.Name,
				Args:                    tc.Function.Arguments,
				Result:                  meta.Result,
				Success:                 &success,
				DurationMs:              meta.DurationMs,
				SandboxParentDurationMs: meta.SandboxParentDurationMs,
			})
		}
	}

	if len(items) == 0 {
		return ""
	}
	data, err := json.Marshal(items)
	if err != nil {
		return ""
	}
	return string(data)
}

// extractAskUserQuestion pulls the prompt text out of an ask_user tool call's
// JSON arguments, tolerating the question/prompt/message field aliases.
func extractAskUserQuestion(arguments string) string {
	var args struct {
		Question string `json:"question"`
		Prompt   string `json:"prompt"`
		Message  string `json:"message"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return ""
	}
	if args.Question != "" {
		return args.Question
	}
	if args.Prompt != "" {
		return args.Prompt
	}
	return args.Message
}
