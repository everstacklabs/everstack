package toolloop

import (
	"context"
	"encoding/json"

	"github.com/everstacklabs/everstack/internal/functions/executor"
	"github.com/everstacklabs/everstack/internal/functions/isolation"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/jmoiron/sqlx"
)

// LoopManager manages the tool execution loop for chat completions.
type LoopManager struct {
	handler          *Handler
	isolatedExecutor *executor.IsolatedExecutor
	enabled          bool
}

// NewLoopManager creates a new tool loop manager.
func NewLoopManager(db *sqlx.DB, enabled bool) *LoopManager {
	return NewLoopManagerWithBackend(db, enabled, nil)
}

// NewLoopManagerWithBackend creates a new tool loop manager with an optional isolation backend.
func NewLoopManagerWithBackend(db *sqlx.DB, enabled bool, backend isolation.Backend) *LoopManager {
	if db == nil || !enabled {
		return &LoopManager{enabled: false}
	}

	lookup := NewDBFunctionLookup(db)

	// Create isolated executor if backend is provided
	var isolatedExec *executor.IsolatedExecutor
	if backend != nil {
		isolatedExec = executor.NewIsolatedExecutor(backend)
	}

	cfg := DefaultConfig()
	cfg.IsolatedExecutor = isolatedExec
	cfg.DB = db

	handler := NewHandler(lookup, cfg)

	return &LoopManager{
		handler:          handler,
		isolatedExecutor: isolatedExec,
		enabled:          enabled,
	}
}

// StartIsolationBackend starts the isolation backend if configured.
func (m *LoopManager) StartIsolationBackend(ctx context.Context) error {
	if m.isolatedExecutor != nil {
		return m.isolatedExecutor.Start(ctx)
	}
	return nil
}

// StopIsolationBackend stops the isolation backend if configured.
func (m *LoopManager) StopIsolationBackend(ctx context.Context) error {
	if m.isolatedExecutor != nil {
		return m.isolatedExecutor.Stop(ctx)
	}
	return nil
}

// IsolationStats returns statistics from the isolation backend.
func (m *LoopManager) IsolationStats() isolation.BackendStats {
	if m.isolatedExecutor != nil {
		return m.isolatedExecutor.Stats()
	}
	return isolation.BackendStats{}
}

// HasIsolationBackend returns whether an isolation backend is configured and available.
func (m *LoopManager) HasIsolationBackend() bool {
	return m.isolatedExecutor != nil
}

// SetBackendResolver sets the backend resolver for per-function Docker host targeting.
func (m *LoopManager) SetBackendResolver(resolver isolation.BackendResolver) {
	if m.isolatedExecutor != nil && resolver != nil {
		m.isolatedExecutor.SetBackendResolver(resolver)
	}
}

// IsEnabled returns whether the tool loop is enabled.
func (m *LoopManager) IsEnabled() bool {
	return m.enabled && m.handler != nil
}

// ShouldExecuteToolLoop checks if the response requires tool execution.
func (m *LoopManager) ShouldExecuteToolLoop(response *gateway.ChatCompletionResponse) bool {
	if !m.IsEnabled() || response == nil || len(response.Choices) == 0 {
		return false
	}

	choice := response.Choices[0]

	// Check finish reason
	if choice.FinishReason == "tool_calls" || choice.FinishReason == "tool_use" {
		return true
	}

	// Check if message has tool calls
	if len(choice.Message.ToolCalls) > 0 {
		return true
	}

	return false
}

// ExtractToolCalls extracts tool calls from a chat completion response.
func (m *LoopManager) ExtractToolCalls(response *gateway.ChatCompletionResponse) []ToolCallMessage {
	if response == nil || len(response.Choices) == 0 {
		return nil
	}

	choice := response.Choices[0]
	if len(choice.Message.ToolCalls) == 0 {
		return nil
	}

	toolCalls := make([]ToolCallMessage, len(choice.Message.ToolCalls))
	for i, tc := range choice.Message.ToolCalls {
		toolCalls[i] = ToolCallMessage{
			ID:   tc.ID,
			Type: tc.Type,
			Function: ToolCallFunction{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		}
	}

	return toolCalls
}

// ExecuteToolLoop runs the tool execution loop.
// Returns updated messages to append to the conversation and continue with the LLM.
func (m *LoopManager) ExecuteToolLoop(
	ctx context.Context,
	execCtx *ExecutionContext,
	response *gateway.ChatCompletionResponse,
) ([]gateway.Message, error) {
	if !m.IsEnabled() {
		return nil, nil
	}

	toolCalls := m.ExtractToolCalls(response)
	if len(toolCalls) == 0 {
		return nil, nil
	}

	logger.WithFields(
		"request_id", execCtx.RequestID,
		"tenant_id", execCtx.TenantID,
		"tool_call_count", len(toolCalls),
		"correlation_id", execCtx.CorrelationID,
	).Info("executing tool loop")

	// Execute tool calls
	results, err := m.handler.ExecuteToolCalls(ctx, execCtx, toolCalls)
	if err != nil {
		logger.WithFields(
			"error", err.Error(),
			"correlation_id", execCtx.CorrelationID,
		).Error("tool loop execution failed")
		// Continue with error results in messages
	}

	// Build messages to append:
	// 1. The assistant message with tool_calls
	// 2. Tool result messages for each tool call

	messages := make([]gateway.Message, 0, len(results)+1)

	// Add assistant message with tool calls
	assistantMsg := gateway.Message{
		Role:      gateway.RoleAssistant,
		ToolCalls: response.Choices[0].Message.ToolCalls,
	}
	// Copy content if any
	if len(response.Choices[0].Message.Content) > 0 {
		assistantMsg.Content = response.Choices[0].Message.Content
	}
	messages = append(messages, assistantMsg)

	// Add tool result messages
	for _, result := range results {
		toolMsg := gateway.Message{
			Role:       gateway.RoleTool,
			ToolCallID: result.ToolCallID,
			Content: []gateway.ContentPart{
				{
					Type: "text",
					Text: stringPtr(result.Content),
				},
			},
		}
		messages = append(messages, toolMsg)
	}

	return messages, nil
}

// BuildToolDefinitions builds tool definitions from registered functions for a tenant.
func (m *LoopManager) BuildToolDefinitions(ctx context.Context, tenantID string, functionNames []string) ([]gateway.ToolDefinition, error) {
	if !m.IsEnabled() {
		return nil, nil
	}

	tools := make([]gateway.ToolDefinition, 0, len(functionNames))

	for _, name := range functionNames {
		fn, err := m.handler.functionLookup.GetFunctionByName(ctx, name, tenantID)
		if err != nil {
			logger.WithFields(
				"function_name", name,
				"tenant_id", tenantID,
				"error", err.Error(),
			).Warn("failed to lookup function for tool definition")
			continue
		}

		// Parse parameters JSON schema
		var params map[string]interface{}
		if len(fn.Parameters) > 0 {
			json.Unmarshal(fn.Parameters, &params)
		}

		description := ""
		if fn.Description.Valid {
			description = fn.Description.String
		}

		tool := gateway.ToolDefinition{
			Type: "function",
			Function: gateway.ToolFunctionDef{
				Name:        fn.Name,
				Description: description,
				Parameters:  params,
			},
		}
		tools = append(tools, tool)
	}

	return tools, nil
}

// ListEnabledFunctionNames returns all enabled function names for a tenant.
// Used by the workflow engine to auto-discover available tools.
func (m *LoopManager) ListEnabledFunctionNames(ctx context.Context, tenantID string) ([]string, error) {
	if !m.IsEnabled() {
		return nil, nil
	}
	dbLookup, ok := m.handler.functionLookup.(*DBFunctionLookup)
	if !ok {
		return nil, nil
	}
	return dbLookup.ListEnabledFunctionNames(ctx, tenantID)
}

// Helper to create string pointer
func stringPtr(s string) *string {
	return &s
}
