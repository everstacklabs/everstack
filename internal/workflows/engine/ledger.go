package engine

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// NodeOutput represents the recorded output of a single node execution.
type NodeOutput struct {
	NodeID     string                 `json:"node_id"`
	NodeType   string                 `json:"node_type"`
	NodeLabel  string                 `json:"node_label"`
	Status     string                 `json:"status"` // "success" | "error"
	Handle     string                 `json:"handle"` // NextHandle returned by the executor
	Data       map[string]interface{} `json:"data"`
	StartedAt  time.Time              `json:"started_at"`
	DurationMs int64                  `json:"duration_ms"`
}

// GetString safely retrieves a string value from the output Data map.
// For non-string values (maps, slices, structs) it JSON-marshals them for
// readable output; scalars fall back to fmt.Sprintf.
func (o *NodeOutput) GetString(key string) string {
	if o == nil || o.Data == nil {
		return ""
	}
	v, ok := o.Data[key]
	if !ok {
		return ""
	}
	switch tv := v.(type) {
	case string:
		return tv
	case []byte:
		return string(tv)
	case map[string]interface{}, []interface{}:
		b, err := json.Marshal(tv)
		if err != nil {
			return fmt.Sprintf("%v", tv)
		}
		return string(b)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// ExecutionLedger is an ordered, node-addressable log of all outputs produced
// during a workflow execution.
type ExecutionLedger struct {
	mu      sync.RWMutex
	entries []*NodeOutput
	byID    map[string]*NodeOutput
	byLabel map[string]*NodeOutput
}

// NewExecutionLedger creates a new empty ledger.
func NewExecutionLedger() *ExecutionLedger {
	return &ExecutionLedger{
		entries: make([]*NodeOutput, 0),
		byID:    make(map[string]*NodeOutput),
		byLabel: make(map[string]*NodeOutput),
	}
}

// Record appends a node output to the ledger.
func (l *ExecutionLedger) Record(output *NodeOutput) {
	if output == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, output)
	if output.NodeID != "" {
		l.byID[output.NodeID] = output
	}
	if output.NodeLabel != "" {
		l.byLabel[output.NodeLabel] = output
	}
}

// Get retrieves a node output by node ID.
func (l *ExecutionLedger) Get(nodeID string) *NodeOutput {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.byID[nodeID]
}

// GetByLabel retrieves a node output by its label.
func (l *ExecutionLedger) GetByLabel(label string) *NodeOutput {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.byLabel[label]
}

// Previous returns the most recently recorded node output.
func (l *ExecutionLedger) Previous() *NodeOutput {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if len(l.entries) == 0 {
		return nil
	}
	return l.entries[len(l.entries)-1]
}

// PreviousOfType returns the most recently recorded node output of a given type.
func (l *ExecutionLedger) PreviousOfType(nodeType string) *NodeOutput {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for i := len(l.entries) - 1; i >= 0; i-- {
		if l.entries[i].NodeType == nodeType {
			return l.entries[i]
		}
	}
	return nil
}

// All returns a copy of all recorded node outputs in order.
func (l *ExecutionLedger) All() []*NodeOutput {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]*NodeOutput, len(l.entries))
	copy(out, l.entries)
	return out
}

// Len returns the number of recorded outputs.
func (l *ExecutionLedger) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.entries)
}

// DataOutputsSinceLastProvider returns all successful data-producing node
// outputs (httpRequest, function, webhook) recorded since the most recent
// provider entry in the ledger.  If no prior provider entry exists, it
// collects from the beginning.
func (l *ExecutionLedger) DataOutputsSinceLastProvider() []*NodeOutput {
	l.mu.RLock()
	defer l.mu.RUnlock()

	dataTypes := map[string]bool{
		"httpRequest": true,
		"function":    true,
		"webhook":     true,
		"memory":      true,
	}

	// Walk backward to find the last provider entry.
	startIdx := 0
	for i := len(l.entries) - 1; i >= 0; i-- {
		if l.entries[i].NodeType == "provider" || l.entries[i].NodeType == "agent" {
			startIdx = i + 1
			break
		}
	}

	var out []*NodeOutput
	for i := startIdx; i < len(l.entries); i++ {
		e := l.entries[i]
		if dataTypes[e.NodeType] && e.Status == "success" {
			out = append(out, e)
		}
	}
	return out
}

// FormatNodeOutputsAsContext formats a slice of data-producing node outputs
// into a human-readable string suitable for injection as LLM context.
func FormatNodeOutputsAsContext(outputs []*NodeOutput) string {
	if len(outputs) == 0 {
		return ""
	}

	var buf string
	buf += "The following data was collected by prior workflow steps and is available for you to use in your response:\n"

	for _, o := range outputs {
		buf += fmt.Sprintf("\n--- %s", o.NodeType)
		if o.NodeLabel != "" {
			buf += fmt.Sprintf(" (%s)", o.NodeLabel)
		}
		buf += " ---\n"

		switch o.NodeType {
		case "httpRequest":
			if v := o.GetString("url"); v != "" {
				buf += fmt.Sprintf("URL: %s\n", v)
			}
			if v := o.GetString("method"); v != "" {
				buf += fmt.Sprintf("Method: %s\n", v)
			}
			if v := o.GetString("status_code"); v != "" {
				buf += fmt.Sprintf("Status: %s\n", v)
			}
			if v := o.GetString("body"); v != "" {
				buf += fmt.Sprintf("Response Body:\n%s\n", truncateString(v, 4000))
			}
		case "function":
			if v := o.GetString("function_name"); v != "" {
				buf += fmt.Sprintf("Function: %s\n", v)
			}
			if v := o.GetString("result"); v != "" {
				buf += fmt.Sprintf("Result:\n%s\n", truncateString(v, 4000))
			}
			if o.DurationMs > 0 {
				buf += fmt.Sprintf("Duration: %dms\n", o.DurationMs)
			}
		case "webhook":
			if v := o.GetString("url"); v != "" {
				buf += fmt.Sprintf("URL: %s\n", v)
			}
			if v := o.GetString("method"); v != "" {
				buf += fmt.Sprintf("Method: %s\n", v)
			}
			if v := o.GetString("status_code"); v != "" {
				buf += fmt.Sprintf("Status: %s\n", v)
			}
			if v := o.GetString("body"); v != "" {
				buf += fmt.Sprintf("Response Body:\n%s\n", truncateString(v, 4000))
			}
		case "memory":
			if v := o.GetString("operation"); v != "" {
				buf += fmt.Sprintf("Operation: %s\n", v)
			}
			if v := o.GetString("collection"); v != "" {
				buf += fmt.Sprintf("Collection: %s\n", v)
			}
			if v := o.GetString("results_text"); v != "" {
				buf += fmt.Sprintf("Results:\n%s\n", truncateString(v, 4000))
			}
		}
	}

	return buf
}

// truncateString truncates s to maxLen characters, appending a marker if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + fmt.Sprintf("... [truncated, %d total characters]", len(s))
}

// MarshalJSON serializes the ledger entries to JSON.
func (l *ExecutionLedger) MarshalJSON() ([]byte, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return json.Marshal(l.entries)
}
