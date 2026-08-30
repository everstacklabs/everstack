package runtime

import (
	"context"
	"encoding/json"

	agentscmd "github.com/everstacklabs/everstack/internal/commands/handlers/agents"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// CheckpointBuilder creates checkpoint functions that persist loop state via CQRS.
type CheckpointBuilder struct {
	sys *cqrs.System
}

// NewCheckpointBuilder creates a new checkpoint builder.
func NewCheckpointBuilder(sys *cqrs.System) *CheckpointBuilder {
	return &CheckpointBuilder{sys: sys}
}

// BuildFunc returns a checkpoint function for a specific session.
// The returned function dispatches a CompleteTurnCommand to persist turn data.
func (b *CheckpointBuilder) BuildFunc(sessionID, userInput string) func(ctx context.Context, state *LoopState) error {
	return func(ctx context.Context, state *LoopState) error {
		if b.sys == nil || b.sys.CommandBus == nil {
			logger.WithFields("session_id", sessionID).Warn("checkpoint: CQRS system not available, skipping")
			return nil
		}

		// Build tool calls JSON, enriched with execution results.
		// Collect all tool calls that have corresponding results in this turn.
		toolCallsJSON := "[]"
		if len(state.ToolResults) > 0 {
			type enrichedToolCall struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
				Result                  string `json:"result,omitempty"`
				Success                 *bool  `json:"success,omitempty"`
				DurationMs              int64  `json:"duration_ms,omitempty"`
				SandboxParentDurationMs int64  `json:"sandbox_parent_duration_ms,omitempty"`
			}
			var enriched []enrichedToolCall
			seen := make(map[string]bool)
			for _, msg := range state.Messages {
				for _, tc := range msg.ToolCalls {
					if _, ok := state.ToolResults[tc.ID]; !ok {
						continue // skip tool calls from earlier turns without results
					}
					if seen[tc.ID] {
						continue
					}
					seen[tc.ID] = true
					etc := enrichedToolCall{
						ID:   tc.ID,
						Type: tc.Type,
					}
					etc.Function.Name = tc.Function.Name
					etc.Function.Arguments = tc.Function.Arguments
					meta := state.ToolResults[tc.ID]
					etc.Result = meta.Result
					if tc.Function.Name == "ask_user" {
						// Persist the user's actual words, not the LLM-steering
						// wrapper baked into the ask_user result.
						etc.Result = DisplayUserAnswer(meta.Result)
					}
					s := meta.Success
					etc.Success = &s
					etc.DurationMs = meta.DurationMs
					etc.SandboxParentDurationMs = meta.SandboxParentDurationMs
					enriched = append(enriched, etc)
				}
			}
			if len(enriched) > 0 {
				if data, err := json.Marshal(enriched); err == nil {
					toolCallsJSON = string(data)
				}
			}
		} else if len(state.Messages) > 0 {
			// Fallback: no results tracked (e.g. no tool calls this turn)
			for i := len(state.Messages) - 1; i >= 0; i-- {
				if len(state.Messages[i].ToolCalls) > 0 {
					if data, err := json.Marshal(state.Messages[i].ToolCalls); err == nil {
						toolCallsJSON = string(data)
					}
					break
				}
			}
		}

		// Build the ordered per-turn timeline (interleaved assistant text,
		// tool calls, and HITL exchanges) so the UI can replay the turn in
		// true chronological order after a reload.
		timelineJSON := buildTurnTimeline(state)

		// Determine session status from loop state
		sessionStatus := "running"
		if state.Done {
			switch state.FinishReason {
			case "error":
				sessionStatus = "failed"
			case "max_iterations", "max_steps", "max_tool_calls", "timeout", "cancelled":
				sessionStatus = "completed" // abnormal termination
			case "explicit_complete":
				sessionStatus = "completed" // user/API requested completion
			case "token_budget_exhausted":
				sessionStatus = "completed"
			default:
				// "stop", "end_turn", "interrupted" = normal turn completion, session stays open
				sessionStatus = "waiting_for_input"
			}
		}

		errMsg := ""
		if state.FinishReason == "error" {
			errMsg = "turn failed"
		}

		// ExpectedTurnCount is the session turn_count before this turn completes.
		// TurnNumber is 1-based and incremented at the start of a turn, so the
		// expected prior count is TurnNumber - 1.
		expectedTurnCount := state.TurnNumber - 1

		cmd := agentscmd.NewCompleteTurnCommand(
			sessionID,
			state.TurnNumber,
			expectedTurnCount,
			userInput,
			state.LastAssistantText,
			toolCallsJSON,
			int32(state.TurnUsage.PromptTokens),
			int32(state.TurnUsage.CompletionTokens),
			int32(state.TurnUsage.TotalTokens),
			0, // latency calculated at turn level
			errMsg,
			sessionStatus,
		)
		cmd.CacheReadInputTokens = int32(state.TurnUsage.CacheReadTokens)
		cmd.CacheWriteInputTokens = int32(state.TurnUsage.CacheWriteTokens)
		cmd.Timeline = timelineJSON

		if err := b.sys.CommandBus.Dispatch(ctx, cmd); err != nil {
			logger.WithFields(
				"session_id", sessionID,
				"turn_number", state.TurnNumber,
				"error", err.Error(),
			).Error("checkpoint: failed to persist turn")
			return err
		}

		logger.WithFields(
			"session_id", sessionID,
			"turn_number", state.TurnNumber,
			"status", sessionStatus,
		).Debug("checkpoint: turn persisted")

		return nil
	}
}
