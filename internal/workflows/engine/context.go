package engine

import (
	"time"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// ExecutionContext is the data bus flowing between nodes during workflow execution.
type ExecutionContext struct {
	// Execution identity
	ExecutionID   string // UUID of this execution record
	CorrelationID string // Format: wfx_{uuid}, links to OTEL traces/logs
	WorkflowID    string // ID of the workflow being executed
	TenantID      string // Tenant that owns the workflow

	// Input
	Messages []gw.Message          // Chat messages from caller
	Metadata map[string]string     // Key-value metadata from request

	// State (modified by nodes as execution progresses)
	Variables        map[string]interface{} // Named variables written by nodes
	ResolvedModel    string                 // Model to use (set by router/provider node)
	ResolvedProvider string                 // Provider name (set by router node)
	SamplingParams   gw.SamplingParams      // Temperature, top_p, etc.

	// Response (set by provider node)
	Response *gw.ChatCompletionResponse

	// Auth state
	Authenticated bool
	AuthToken     string

	// Branching flags
	CacheHit        bool
	CacheMiss       bool   // Set by cache executor on miss; triggers post-provider cache write
	CacheType       string // "exact" or "semantic" — used for post-provider cache write
	CacheQuery      string // Query text for semantic cache write
	InputBlocked    bool
	OutputBlocked   bool
	ConditionResult bool

	// Streaming
	StreamingEnabled bool
	OnEvent          func(ExecutionEvent) error

	// Timing
	StartTime  time.Time
	NodeTimings map[string]int64 // nodeID -> duration in ms

	// Per-node execution details (cleared between nodes, copied into events)
	NodeData map[string]string

	// Execution ledger — ordered log of all node outputs
	Ledger *ExecutionLedger

	// OriginalMessages is a snapshot of the input messages before the start
	// node modifies them (e.g., prepending system prompt, applying templates).
	OriginalMessages []gw.Message
}

// NewExecutionContext creates a new execution context with defaults.
func NewExecutionContext() *ExecutionContext {
	return &ExecutionContext{
		Variables:   make(map[string]interface{}),
		Metadata:    make(map[string]string),
		NodeTimings: make(map[string]int64),
		NodeData:    make(map[string]string),
		Ledger:      NewExecutionLedger(),
		StartTime:   time.Now(),
	}
}

// SetNodeData sets a key-value pair in the per-node execution data.
func (ec *ExecutionContext) SetNodeData(key, value string) {
	ec.NodeData[key] = value
}

// ClearNodeData resets the per-node execution data for the next node.
func (ec *ExecutionContext) ClearNodeData() {
	ec.NodeData = make(map[string]string)
}

// SetVariable sets a named variable in the execution context.
func (ec *ExecutionContext) SetVariable(name string, value interface{}) {
	ec.Variables[name] = value
}

// GetVariable retrieves a named variable from the execution context.
func (ec *ExecutionContext) GetVariable(name string) (interface{}, bool) {
	v, ok := ec.Variables[name]
	return v, ok
}

// GetStringVariable retrieves a named variable as a string.
func (ec *ExecutionContext) GetStringVariable(name string) string {
	v, ok := ec.Variables[name]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// SnapshotOriginalMessages captures a copy of ec.Messages before modification.
// Should be called once at the start of execution before the start node modifies messages.
func (ec *ExecutionContext) SnapshotOriginalMessages() {
	if ec.OriginalMessages != nil {
		return // Already snapshotted
	}
	ec.OriginalMessages = make([]gw.Message, len(ec.Messages))
	copy(ec.OriginalMessages, ec.Messages)
}

// OriginalUserInput returns the text content of the last user message from the
// original (pre-modification) messages snapshot.
func (ec *ExecutionContext) OriginalUserInput() string {
	msgs := ec.OriginalMessages
	if len(msgs) == 0 {
		msgs = ec.Messages
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == gw.RoleUser && len(msgs[i].Content) > 0 && msgs[i].Content[0].Text != nil {
			return *msgs[i].Content[0].Text
		}
	}
	return ""
}

// LastAssistantContent returns the text content from the last assistant message,
// or from the response if set.
func (ec *ExecutionContext) LastAssistantContent() string {
	if ec.Response != nil && len(ec.Response.Choices) > 0 {
		msg := ec.Response.Choices[0].Message
		if len(msg.Content) > 0 && msg.Content[0].Text != nil {
			return *msg.Content[0].Text
		}
	}
	// Fallback: scan messages in reverse for last assistant message
	for i := len(ec.Messages) - 1; i >= 0; i-- {
		if ec.Messages[i].Role == gw.RoleAssistant && len(ec.Messages[i].Content) > 0 {
			if ec.Messages[i].Content[0].Text != nil {
				return *ec.Messages[i].Content[0].Text
			}
		}
	}
	return ""
}
