package engine

import "time"

// ExecutionEvent represents a single event emitted during workflow execution.
type ExecutionEvent struct {
	Type         string            // node.started, node.completed, node.error, chunk, done, error
	NodeID       string            // ID of the node that emitted this event
	NodeType     string            // Type of the node (e.g., "provider", "start")
	NodeLabel    string            // Human-readable label
	Data         map[string]string // Additional metadata
	ChunkContent string            // LLM streaming token content (for type=chunk)
	Error        string            // Error message (for type=node.error or error)
	Timestamp    time.Time         // When the event occurred
	DurationMs   int64             // Duration in milliseconds (for completed/error events)
}
