// Package executor provides tool execution implementations for serverless functions.
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ExecutionMode represents how a function is executed.
type ExecutionMode string

const (
	ModeWebhook  ExecutionMode = "webhook"
	ModeProxy    ExecutionMode = "proxy"
	ModeIsolated ExecutionMode = "isolated"
)

// ToolCall represents a tool call from an LLM.
type ToolCall struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// ToolResult represents the result of a tool execution.
type ToolResult struct {
	ToolCallID string      `json:"tool_call_id"`
	Content    interface{} `json:"content"`
	Error      string      `json:"error,omitempty"`
	Success    bool        `json:"success"`
	DurationMs int64       `json:"duration_ms"`
}

// FunctionConfig represents a function's execution configuration.
type FunctionConfig struct {
	ID          string
	TenantID    string
	Name        string
	Mode        ExecutionMode
	TimeoutMs   int32
	MaxRetries  int32
	Parameters  map[string]interface{} // JSON Schema for validation

	// Webhook mode config
	WebhookURL       string
	WebhookMethod    string
	WebhookHeaders   map[string]string
	WebhookTimeoutMs int32

	// Proxy mode config
	ProxyBaseURL         string
	ProxyPath            string
	ProxyMethod          string
	ProxyQueryMapping    map[string]string
	ProxyHeaderMapping   map[string]string
	ProxyBodyMapping     map[string]string
	ProxyResponseMapping map[string]string

	// Isolated mode config
	Runtime      string
	Code         string
	Packages     []string
	NetworkMode  string   // "deny", "whitelist", "allow"
	AllowedHosts []string // For whitelist mode
	VCPUs        int
	MemoryMB     int
	DockerHost   string
}

// ExecutionContext provides context for tool execution.
type ExecutionContext struct {
	RequestID     string
	TenantID      string
	UserID        string
	CorrelationID string
	TraceID       string
	Timeout       time.Duration
}

// Executor is the interface for tool executors.
type Executor interface {
	// Execute runs the tool with the given arguments and returns the result.
	Execute(ctx context.Context, execCtx *ExecutionContext, config *FunctionConfig, args map[string]interface{}) (*ToolResult, error)

	// Mode returns the execution mode this executor handles.
	Mode() ExecutionMode
}

// Registry manages function configurations and executor routing.
type Registry struct {
	executors map[ExecutionMode]Executor
}

// NewRegistry creates a new executor registry.
func NewRegistry() *Registry {
	return &Registry{
		executors: make(map[ExecutionMode]Executor),
	}
}

// RegisterExecutor registers an executor for a specific mode.
func (r *Registry) RegisterExecutor(executor Executor) {
	r.executors[executor.Mode()] = executor
}

// Execute routes the tool call to the appropriate executor.
func (r *Registry) Execute(ctx context.Context, execCtx *ExecutionContext, config *FunctionConfig, toolCall *ToolCall) (*ToolResult, error) {
	executor, ok := r.executors[config.Mode]
	if !ok {
		return &ToolResult{
			ToolCallID: toolCall.ID,
			Success:    false,
			Error:      fmt.Sprintf("no executor registered for mode: %s", config.Mode),
		}, fmt.Errorf("no executor registered for mode: %s", config.Mode)
	}

	start := time.Now()
	result, err := executor.Execute(ctx, execCtx, config, toolCall.Arguments)
	if result != nil {
		result.ToolCallID = toolCall.ID
		result.DurationMs = time.Since(start).Milliseconds()
	}

	return result, err
}

// ParseToolCallArguments parses JSON arguments string into a map.
// If the JSON is truncated (e.g. LLM hit max_tokens), it attempts repair.
func ParseToolCallArguments(argsJSON string) (map[string]interface{}, error) {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		// Attempt JSON repair for truncated output
		repaired := RepairJSON(argsJSON)
		if repaired != argsJSON {
			if err2 := json.Unmarshal([]byte(repaired), &args); err2 == nil {
				return args, nil
			}
		}
		truncated := argsJSON
		if len(truncated) > 200 {
			truncated = truncated[:200] + "..."
		}
		return nil, fmt.Errorf("failed to parse tool arguments (malformed JSON — output was likely truncated). Truncated input: %s", truncated)
	}
	return args, nil
}

// RepairJSON attempts to fix common JSON truncation patterns caused by the LLM
// hitting its output token limit mid-generation.
func RepairJSON(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || json.Valid([]byte(s)) {
		return s
	}

	inString := false
	escaped := false
	var stack []byte

	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && inString {
			escaped = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch c {
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if len(stack) > 0 && stack[len(stack)-1] == c {
				stack = stack[:len(stack)-1]
			}
		}
	}

	if inString {
		s += `"`
	}

	trimmed := strings.TrimRight(s, " \t\n\r")
	if len(trimmed) > 0 && trimmed[len(trimmed)-1] == ',' {
		s = trimmed[:len(trimmed)-1]
	}

	for i := len(stack) - 1; i >= 0; i-- {
		s += string(stack[i])
	}

	return s
}

// FormatToolResult formats a tool result for inclusion in LLM messages.
func FormatToolResult(result *ToolResult) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	switch v := result.Content.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		// JSON encode the result
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("Error encoding result: %v", err)
		}
		return string(data)
	}
}
