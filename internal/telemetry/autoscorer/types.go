package autoscorer

import (
	"context"

	"github.com/everstacklabs/everstack/internal/telemetry/scores"
)

// ScoreRecorder is the interface for persisting scores.
// Matches *scores.Recorder but allows testing with mocks.
type ScoreRecorder interface {
	RecordBatch(ctx context.Context, scores []*scores.Score) error
}

// TurnContext holds all the data a scorer needs to evaluate a single turn.
type TurnContext struct {
	TraceID       string
	SessionID     string
	AgentID       string
	TenantID      string
	TurnNumber    int32
	UserInput     string
	AssistantText string
	FinishReason  string

	// Tool execution summary
	ToolCalls        int
	SandboxToolCalls int
	ToolErrors       int
	IterationCount   int32

	// Token usage
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int

	// Per-tool details (keyed by tool_call_id)
	ToolResults map[string]ToolResult
}

// ToolResult captures the outcome of a single tool call.
type ToolResult struct {
	Name       string
	Args       string
	Result     string
	Success    bool
	DurationMs int64
}

// Scorer computes one or more scores from a TurnContext.
// Each scorer is independent and should not block.
type Scorer interface {
	// Name returns a unique identifier for this scorer (used as score name prefix).
	Name() string
	// Score evaluates the turn and returns zero or more scores.
	Score(ctx context.Context, tc *TurnContext) []*scores.Score
}
