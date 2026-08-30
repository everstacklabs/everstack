package executors

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/functions/executor"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
	functionsquery "github.com/everstacklabs/everstack/internal/query/handlers/functions"
	"github.com/everstacklabs/everstack/internal/workflows/engine"
)

// FunctionExecutor handles serverless function execution nodes.
//
// Config fields (from frontend FunctionConfig):
//   - functionId: UUID of the function to execute (required)
//   - functionName: display name (informational)
//   - functionMode: "webhook" | "proxy" | "isolated" (informational, loaded from DB)
//   - parameterMappings: map of parameter name → source (e.g., "input" → "$messages.last")
//   - timeoutMs: execution timeout in milliseconds (default: 30000)
//
// The executor loads the function configuration from the database, maps input
// parameters from the execution context, invokes the appropriate executor
// (webhook, proxy, or isolated), and stores the result as a variable.
//
// Handles: "out" on success. Returns an error on execution failure.
type FunctionExecutor struct {
	ServerCtx context.Context // Server context with CQRS system
}

func (e *FunctionExecutor) NodeType() string { return "function" }

func (e *FunctionExecutor) Execute(ctx context.Context, node *engine.GraphNode, ec *engine.ExecutionContext) engine.NodeResult {
	functionID := node.GetConfigString("functionId")
	if functionID == "" {
		return engine.NodeResult{Error: fmt.Errorf("function node has no functionId configured")}
	}

	// Look up function via CQRS query
	fn, err := e.loadFunction(ctx, functionID)
	if err != nil {
		return engine.NodeResult{Error: fmt.Errorf("failed to load function %s: %w", functionID, err)}
	}

	// Build execution config from the database model
	funcConfig := buildFunctionConfig(fn)

	// Apply timeout from node config if specified
	timeoutMs := node.GetConfigInt("timeoutMs")
	if timeoutMs > 0 {
		funcConfig.TimeoutMs = int32(timeoutMs)
	}

	// Build arguments from parameter mappings and execution context
	args := e.buildArguments(node, ec)

	// Build execution context
	tenantID := contextkeys.GetTenantID(ctx)
	execCtx := &executor.ExecutionContext{
		RequestID:     uuid.New().String(),
		TenantID:      tenantID,
		CorrelationID: uuid.New().String()[:8],
		Timeout:       time.Duration(funcConfig.TimeoutMs) * time.Millisecond,
	}

	logger.WithFields(
		"function_id", functionID,
		"function_name", fn.Name,
		"mode", fn.Mode,
		"timeout_ms", funcConfig.TimeoutMs,
	).Debug("function executor: executing function")

	// Create the appropriate executor and run it
	result, execErr := e.executeFunction(ctx, execCtx, funcConfig, args)
	if execErr != nil {
		logger.WithFields(
			"function_id", functionID,
			"error", execErr.Error(),
		).Error("function executor: execution failed")
		return engine.NodeResult{Error: fmt.Errorf("function %s execution failed: %w", fn.Name, execErr)}
	}

	if result != nil && !result.Success {
		logger.WithFields(
			"function_id", functionID,
			"error", result.Error,
		).Warn("function executor: function returned error")
		return engine.NodeResult{Error: fmt.Errorf("function %s returned error: %s", fn.Name, result.Error)}
	}

	// Store result in execution context variables
	ec.SetVariable("function_name", fn.Name)
	ec.SetVariable("function_id", fn.ID)
	ec.SetVariable("function_mode", fn.Mode)
	if result != nil {
		ec.SetVariable("function_result", result.Content)
		ec.SetVariable("function_duration_ms", result.DurationMs)

		// Format result as string for potential use in messages
		resultStr := executor.FormatToolResult(result)
		ec.SetVariable("function_result_text", resultStr)
	}

	ec.SetNodeData("function_name", fn.Name)
	ec.SetNodeData("success", "true")
	if result != nil {
		preview := fmt.Sprintf("%v", result.Content)
		if len(preview) > 200 {
			preview = preview[:200]
		}
		ec.SetNodeData("result_preview", preview)
	}

	logger.WithFields(
		"function_id", functionID,
		"function_name", fn.Name,
		"duration_ms", result.DurationMs,
	).Debug("function executor: execution completed")

	output := map[string]interface{}{
		"function_name": fn.Name,
		"function_id":   fn.ID,
		"success":       true,
	}
	if result != nil {
		output["result"] = result.Content
		output["duration_ms"] = result.DurationMs
	}

	return engine.NodeResult{NextHandle: "out", Output: output}
}

// loadFunction retrieves the function configuration from the database.
func (e *FunctionExecutor) loadFunction(ctx context.Context, functionID string) (*functionsquery.FunctionReadModel, error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && e.ServerCtx != nil {
		sys, err = cqrs.GetSystemFromContext(e.ServerCtx)
	}
	if err != nil {
		return nil, fmt.Errorf("CQRS system not available: %w", err)
	}

	tenantID := contextkeys.GetTenantID(ctx)

	q := functionsquery.NewGetFunctionByIDQuery(functionID, tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, fmt.Errorf("function not found")
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}

	fn, ok := data.(*functionsquery.FunctionReadModel)
	if !ok {
		return nil, fmt.Errorf("unexpected data type for function")
	}

	return fn, nil
}

// buildFunctionConfig converts a FunctionReadModel to an executor.FunctionConfig.
func buildFunctionConfig(fn *functionsquery.FunctionReadModel) *executor.FunctionConfig {
	cfg := &executor.FunctionConfig{
		ID:        fn.ID,
		TenantID:  fn.TenantID,
		Name:      fn.Name,
		Mode:      executor.ExecutionMode(fn.Mode),
		TimeoutMs: fn.TimeoutMs,
	}

	// Webhook config
	if fn.WebhookURL.Valid {
		cfg.WebhookURL = fn.WebhookURL.String
	}
	if fn.WebhookMethod.Valid {
		cfg.WebhookMethod = fn.WebhookMethod.String
	}
	if fn.WebhookTimeoutMs.Valid {
		cfg.WebhookTimeoutMs = fn.WebhookTimeoutMs.Int32
	}
	if len(fn.WebhookHeaders) > 0 {
		var headers map[string]string
		if err := json.Unmarshal(fn.WebhookHeaders, &headers); err == nil {
			cfg.WebhookHeaders = headers
		}
	}

	// Proxy config
	if fn.ProxyBaseURL.Valid {
		cfg.ProxyBaseURL = fn.ProxyBaseURL.String
	}
	if fn.ProxyPath.Valid {
		cfg.ProxyPath = fn.ProxyPath.String
	}
	if fn.ProxyMethod.Valid {
		cfg.ProxyMethod = fn.ProxyMethod.String
	}
	if len(fn.ProxyQueryMapping) > 0 {
		var m map[string]string
		if err := json.Unmarshal(fn.ProxyQueryMapping, &m); err == nil {
			cfg.ProxyQueryMapping = m
		}
	}
	if len(fn.ProxyHeaderMapping) > 0 {
		var m map[string]string
		if err := json.Unmarshal(fn.ProxyHeaderMapping, &m); err == nil {
			cfg.ProxyHeaderMapping = m
		}
	}
	if len(fn.ProxyBodyMapping) > 0 {
		var m map[string]string
		if err := json.Unmarshal(fn.ProxyBodyMapping, &m); err == nil {
			cfg.ProxyBodyMapping = m
		}
	}
	if len(fn.ProxyResponseMapping) > 0 {
		var m map[string]string
		if err := json.Unmarshal(fn.ProxyResponseMapping, &m); err == nil {
			cfg.ProxyResponseMapping = m
		}
	}

	// Isolated config
	if fn.Runtime.Valid {
		cfg.Runtime = fn.Runtime.String
	}
	if fn.Code.Valid {
		cfg.Code = fn.Code.String
	}
	cfg.Packages = fn.Packages
	if fn.NetworkMode.Valid {
		cfg.NetworkMode = fn.NetworkMode.String
	}
	cfg.AllowedHosts = fn.AllowedHosts
	cfg.VCPUs = int(fn.VCPUs)
	cfg.MemoryMB = int(fn.MemoryMB)
	if fn.DockerHost.Valid {
		cfg.DockerHost = fn.DockerHost.String
	}

	// Parameters
	if len(fn.Parameters) > 0 {
		var params map[string]interface{}
		if err := json.Unmarshal(fn.Parameters, &params); err == nil {
			cfg.Parameters = params
		}
	}

	return cfg
}

// buildArguments maps parameters from the execution context to function arguments.
func (e *FunctionExecutor) buildArguments(node *engine.GraphNode, ec *engine.ExecutionContext) map[string]interface{} {
	args := make(map[string]interface{})

	// Check for parameterMappings config
	if node.Config != nil {
		if mappingsRaw, ok := node.Config["parameterMappings"]; ok {
			if mappings, ok := mappingsRaw.(map[string]interface{}); ok {
				for paramName, sourceRaw := range mappings {
					source, ok := sourceRaw.(string)
					if !ok {
						continue
					}
					args[paramName] = resolveParameterSource(source, ec)
				}
			}
		}
	}

	// Always include standard context variables
	if _, exists := args["messages"]; !exists {
		// Include the last user message as default input
		if query := extractUserQuery(ec); query != "" {
			args["input"] = query
		}
	}

	return args
}

// resolveParameterSource resolves a parameter source expression to a value.
func resolveParameterSource(source string, ec *engine.ExecutionContext) interface{} {
	switch source {
	case "$messages.last":
		return extractUserQuery(ec)
	case "$messages.all":
		return ec.Messages
	case "$auth.token":
		return ec.AuthToken
	case "$provider":
		return ec.ResolvedProvider
	case "$model":
		return ec.ResolvedModel
	default:
		// Try ledger expression resolver for $ prefixed sources
		if strings.HasPrefix(source, "$") && ec.Ledger != nil {
			resolved := ec.Ledger.Resolve(source, ec)
			if resolved != source {
				return resolved
			}
		}
		// Try as a variable reference ($var.name)
		if len(source) > 5 && source[:5] == "$var." {
			varName := source[5:]
			if v, ok := ec.GetVariable(varName); ok {
				return v
			}
		}
		// Return as literal value
		return source
	}
}

// executeFunction creates the appropriate executor and runs the function.
func (e *FunctionExecutor) executeFunction(ctx context.Context, execCtx *executor.ExecutionContext, config *executor.FunctionConfig, args map[string]interface{}) (*executor.ToolResult, error) {
	reg := executor.NewRegistry()
	reg.RegisterExecutor(executor.NewWebhookExecutor())
	reg.RegisterExecutor(executor.NewProxyExecutor())

	toolCall := &executor.ToolCall{
		ID:        uuid.New().String(),
		Name:      config.Name,
		Arguments: args,
	}

	return reg.Execute(ctx, execCtx, config, toolCall)
}
