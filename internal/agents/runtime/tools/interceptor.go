package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	"github.com/everstacklabs/everstack/internal/functions/toolloop"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// SyntheticToolHandler handles a synthetic (non-DB-backed) tool call.
type SyntheticToolHandler interface {
	// Name returns the tool function name.
	Name() string
	// Definition returns the tool definition for LLM function calling.
	Definition() gw.ToolDefinition
	// Execute handles the tool call and returns the result string.
	Execute(ctx context.Context, args map[string]interface{}) (string, error)
}

// ToolInterceptor wraps a LoopManager to intercept synthetic tool calls.
// Synthetic tools (spawn_agent, memory_store, memory_query) are handled
// directly, while regular tools pass through to the underlying LoopManager.
type ToolInterceptor struct {
	Inner    *toolloop.LoopManager
	Handlers map[string]SyntheticToolHandler

	// AlwaysInclude contains tool names that bypass the agent's configured
	// tools allowlist. Used for contextual tools injected at runtime (e.g.
	// read_channel_history in channel sessions).
	AlwaysInclude map[string]struct{}
}

// NewToolInterceptor creates a new interceptor wrapping the given LoopManager.
func NewToolInterceptor(inner *toolloop.LoopManager) *ToolInterceptor {
	return &ToolInterceptor{
		Inner:         inner,
		Handlers:      make(map[string]SyntheticToolHandler),
		AlwaysInclude: make(map[string]struct{}),
	}
}

// RegisterHandler adds a synthetic tool handler.
func (ti *ToolInterceptor) RegisterHandler(handler SyntheticToolHandler) {
	ti.Handlers[handler.Name()] = handler
}

// RegisterAlwaysInclude registers a handler that bypasses the agent's tool allowlist.
func (ti *ToolInterceptor) RegisterAlwaysInclude(handler SyntheticToolHandler) {
	ti.Handlers[handler.Name()] = handler
	ti.AlwaysInclude[handler.Name()] = struct{}{}
}

// IsSyntheticTool checks if a tool name is handled by a synthetic handler.
func (ti *ToolInterceptor) IsSyntheticTool(name string) bool {
	_, ok := ti.Handlers[name]
	return ok
}

// ExecuteSyntheticTool executes a synthetic tool call by name.
func (ti *ToolInterceptor) ExecuteSyntheticTool(ctx context.Context, name string, argsJSON string) (string, error) {
	handler, ok := ti.Handlers[name]
	if !ok {
		return "", fmt.Errorf("no synthetic handler for tool: %s", name)
	}

	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		// Attempt JSON repair for truncated output (e.g. LLM hit max_tokens mid-tool-call)
		repaired := repairJSON(argsJSON)
		if repaired != argsJSON {
			if err2 := json.Unmarshal([]byte(repaired), &args); err2 == nil {
				logger.WithFields("tool", name).Warn("interceptor: repaired truncated JSON tool arguments")
				goto parsed
			}
		}
		// Return an instructive error so the LLM can self-correct
		truncated := argsJSON
		if len(truncated) > 200 {
			truncated = truncated[:200] + "..."
		}
		return "", fmt.Errorf("failed to parse tool arguments (malformed JSON — your output was likely truncated). Please retry the tool call with valid, complete JSON. Truncated input was: %s", truncated)
	}
parsed:

	logger.WithFields("tool", name).Debug("executing synthetic tool")

	result, err := handler.Execute(ctx, args)
	if err != nil {
		return "", err
	}

	return result, nil
}

// repairJSON attempts to fix common JSON truncation patterns caused by the LLM
// hitting its output token limit mid-generation. It handles:
// - Unclosed strings (missing closing quote)
// - Unclosed objects/arrays (missing closing braces/brackets)
// - Trailing commas before closing delimiters
func repairJSON(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}

	// If it already parses, no repair needed
	if json.Valid([]byte(s)) {
		return s
	}

	// Track open delimiters
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

	// If we're inside a string, close it
	if inString {
		s += `"`
	}

	// Remove trailing comma before we close delimiters
	trimmed := strings.TrimRight(s, " \t\n\r")
	if len(trimmed) > 0 && trimmed[len(trimmed)-1] == ',' {
		s = trimmed[:len(trimmed)-1]
	}

	// Close any unclosed delimiters in reverse order
	for i := len(stack) - 1; i >= 0; i-- {
		s += string(stack[i])
	}

	return s
}

// BuildToolDefinitions returns tool definitions including synthetic tools.
func (ti *ToolInterceptor) BuildToolDefinitions(ctx context.Context, tenantID string, functionNames []string) ([]gw.ToolDefinition, error) {
	// Filter out synthetic tool names before passing to the inner manager
	// so it doesn't try (and fail) to look them up in the DB.
	var regularNames []string
	for _, name := range functionNames {
		if _, isSynthetic := ti.Handlers[name]; !isSynthetic {
			regularNames = append(regularNames, name)
		}
	}

	// Get regular tool definitions from the inner manager
	var tools []gw.ToolDefinition
	if ti.Inner != nil && ti.Inner.IsEnabled() && len(regularNames) > 0 {
		var err error
		tools, err = ti.Inner.BuildToolDefinitions(ctx, tenantID, regularNames)
		if err != nil {
			logger.WithFields("error", err.Error()).Warn("interceptor: failed to build regular tool definitions")
		}
	}

	// Build allowlist from requested function names (if provided). This keeps
	// synthetic tools aligned with the agent's configured tools and reduces
	// model confusion from seeing tools it cannot/should not use in this turn.
	allowed := make(map[string]struct{}, len(functionNames))
	if len(functionNames) > 0 {
		for _, name := range functionNames {
			allowed[name] = struct{}{}
		}
	}

	// Append synthetic tool definitions
	for _, handler := range ti.Handlers {
		if len(allowed) > 0 {
			if _, alwaysOk := ti.AlwaysInclude[handler.Name()]; !alwaysOk {
				if _, ok := allowed[handler.Name()]; !ok {
					continue
				}
			}
		}
		tools = append(tools, handler.Definition())
	}

	return tools, nil
}

// ProcessToolCalls checks a response for synthetic tool calls and handles them.
// Returns tool result messages for synthetic calls, and flags indicating
// whether there are remaining regular tool calls to process.
func (ti *ToolInterceptor) ProcessToolCalls(
	ctx context.Context,
	resp *gw.ChatCompletionResponse,
	parentInput *agentrt.LoopInput,
) (syntheticResults []gw.Message, hasRegularTools bool) {
	if resp == nil || len(resp.Choices) == 0 {
		return nil, false
	}

	choice := resp.Choices[0]
	if len(choice.Message.ToolCalls) == 0 {
		return nil, false
	}

	for _, tc := range choice.Message.ToolCalls {
		if ti.IsSyntheticTool(tc.Function.Name) {
			result, err := ti.ExecuteSyntheticTool(ctx, tc.Function.Name, tc.Function.Arguments)
			if err != nil {
				result = fmt.Sprintf("Error: %s", err.Error())
			}

			resultMsg := gw.Message{
				Role:       gw.RoleTool,
				ToolCallID: tc.ID,
				Content: []gw.ContentPart{
					{Type: "text", Text: &result},
				},
			}
			syntheticResults = append(syntheticResults, resultMsg)
		} else {
			hasRegularTools = true
		}
	}

	return syntheticResults, hasRegularTools
}

// portURLProvider is implemented by handlers that track exposed port URLs.
type portURLProvider interface {
	GetPortURLs() []string
}

// CollectExposedURLs returns all sandbox port URLs registered by handlers.
func (ti *ToolInterceptor) CollectExposedURLs() []string {
	for _, handler := range ti.Handlers {
		// Sandbox handlers share a single SandboxSessionContext, so the first
		// match has all the URLs. Check for a wrapper that exposes the context.
		if p, ok := handler.(portURLProvider); ok {
			return p.GetPortURLs()
		}
	}
	return nil
}
