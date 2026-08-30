// Package toolloop provides the tool execution loop for LLM conversations.
package toolloop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	usagecmd "github.com/everstacklabs/everstack/internal/commands/handlers/usage"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/functions/executor"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	functionsquery "github.com/everstacklabs/everstack/internal/query/handlers/functions"
	"github.com/everstacklabs/everstack/internal/usage"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// MaxToolIterations is the maximum number of tool call iterations allowed.
const MaxToolIterations = 10

// Handler manages the tool execution loop.
type Handler struct {
	registry        *executor.Registry
	functionLookup  FunctionLookup
	maxIterations   int
	parallelExecute bool
	db              *sqlx.DB
}

// FunctionLookup provides function configuration lookup.
type FunctionLookup interface {
	GetFunctionByName(ctx context.Context, name, tenantID string) (*functionsquery.FunctionReadModel, error)
}

// DBFunctionLookup implements FunctionLookup using database queries.
type DBFunctionLookup struct {
	db *sqlx.DB
}

// NewDBFunctionLookup creates a new database-backed function lookup.
func NewDBFunctionLookup(db *sqlx.DB) *DBFunctionLookup {
	return &DBFunctionLookup{db: db}
}

// GetFunctionByName retrieves a function by name from the database.
func (l *DBFunctionLookup) GetFunctionByName(ctx context.Context, name, tenantID string) (*functionsquery.FunctionReadModel, error) {
	var fn functionsquery.FunctionReadModel
	err := l.db.GetContext(ctx, &fn, `
		SELECT * FROM functions
		WHERE name = $1 AND enabled = true
	`, name)
	if err != nil {
		return nil, err
	}
	return &fn, nil
}

// ListEnabledFunctionNames returns the names of all enabled functions for a tenant.
func (l *DBFunctionLookup) ListEnabledFunctionNames(ctx context.Context, tenantID string) ([]string, error) {
	var names []string
	err := l.db.SelectContext(ctx, &names, `
		SELECT name FROM functions
		WHERE enabled = true
		ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	return names, nil
}

// Config holds handler configuration.
type Config struct {
	MaxIterations    int
	ParallelExecute  bool
	IsolatedExecutor *executor.IsolatedExecutor // Optional: pre-configured isolated executor
	DB               *sqlx.DB
}

// DefaultConfig returns the default handler configuration.
func DefaultConfig() Config {
	return Config{
		MaxIterations:   MaxToolIterations,
		ParallelExecute: true,
	}
}

// NewHandler creates a new tool loop handler.
func NewHandler(lookup FunctionLookup, cfg Config) *Handler {
	reg := executor.NewRegistry()
	reg.RegisterExecutor(executor.NewWebhookExecutor())
	reg.RegisterExecutor(executor.NewProxyExecutor())

	// Register isolated executor if provided
	if cfg.IsolatedExecutor != nil {
		reg.RegisterExecutor(cfg.IsolatedExecutor)
		logger.Info("isolated function executor registered")
	}

	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = MaxToolIterations
	}

	return &Handler{
		registry:        reg,
		functionLookup:  lookup,
		maxIterations:   maxIter,
		parallelExecute: cfg.ParallelExecute,
		db:              cfg.DB,
	}
}

// ToolCallMessage represents a tool call from an LLM response.
type ToolCallMessage struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // "function"
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction contains the function name and arguments.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ToolResultMessage represents a tool result to send back to the LLM.
type ToolResultMessage struct {
	Role       string `json:"role"` // "tool"
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
}

// ExecutionContext provides context for the tool loop.
type ExecutionContext struct {
	RequestID     string
	TenantID      string
	UserID        string
	CorrelationID string
	TraceID       string
}

// ExecuteToolCalls executes the given tool calls and returns tool result messages.
func (h *Handler) ExecuteToolCalls(ctx context.Context, execCtx *ExecutionContext, toolCalls []ToolCallMessage) ([]ToolResultMessage, error) {
	if len(toolCalls) == 0 {
		return nil, nil
	}

	logger.WithFields(
		"request_id", execCtx.RequestID,
		"tenant_id", execCtx.TenantID,
		"tool_call_count", len(toolCalls),
		"correlation_id", execCtx.CorrelationID,
	).Debug("executing tool calls")

	results := make([]ToolResultMessage, len(toolCalls))

	if h.parallelExecute && len(toolCalls) > 1 {
		// Execute in parallel
		var wg sync.WaitGroup
		var mu sync.Mutex
		errors := make([]error, len(toolCalls))

		for i, tc := range toolCalls {
			wg.Add(1)
			go func(idx int, toolCall ToolCallMessage) {
				defer wg.Done()
				result, err := h.executeToolCall(ctx, execCtx, toolCall)
				mu.Lock()
				results[idx] = result
				errors[idx] = err
				mu.Unlock()
			}(i, tc)
		}
		wg.Wait()

		// Check for any critical errors
		for _, err := range errors {
			if err != nil {
				logger.WithFields(
					"error", err.Error(),
					"correlation_id", execCtx.CorrelationID,
				).Warn("tool call execution had errors")
			}
		}
	} else {
		// Execute sequentially
		for i, tc := range toolCalls {
			result, err := h.executeToolCall(ctx, execCtx, tc)
			results[i] = result
			if err != nil {
				logger.WithFields(
					"error", err.Error(),
					"tool_call_id", tc.ID,
					"function_name", tc.Function.Name,
					"correlation_id", execCtx.CorrelationID,
				).Warn("tool call execution failed")
			}
		}
	}

	return results, nil
}

// executeToolCall executes a single tool call.
func (h *Handler) executeToolCall(ctx context.Context, execCtx *ExecutionContext, toolCall ToolCallMessage) (ToolResultMessage, error) {
	startedAt := time.Now().UTC()

	// Default error result
	errorResult := func(err string) ToolResultMessage {
		return ToolResultMessage{
			Role:       "tool",
			ToolCallID: toolCall.ID,
			Content:    fmt.Sprintf(`{"error": "%s"}`, err),
		}
	}

	// Look up function configuration
	fn, err := h.functionLookup.GetFunctionByName(ctx, toolCall.Function.Name, execCtx.TenantID)
	if err != nil {
		logger.WithFields(
			"function_name", toolCall.Function.Name,
			"tenant_id", execCtx.TenantID,
			"error", err.Error(),
			"correlation_id", execCtx.CorrelationID,
		).Warn("function not found")
		return errorResult(fmt.Sprintf("function '%s' not found or not enabled", toolCall.Function.Name)), err
	}

	// Parse arguments
	args, err := executor.ParseToolCallArguments(toolCall.Function.Arguments)
	if err != nil {
		logger.WithFields(
			"function_name", toolCall.Function.Name,
			"arguments", toolCall.Function.Arguments,
			"error", err.Error(),
			"correlation_id", execCtx.CorrelationID,
		).Warn("failed to parse tool call arguments")
		h.recordFunctionExecutionAndUsage(
			ctx,
			execCtx,
			fn,
			toolCall,
			args,
			nil,
			"validation_error",
			err.Error(),
			startedAt,
		)
		return errorResult(fmt.Sprintf("invalid arguments: %v", err)), err
	}

	// Build function config
	config := h.buildFunctionConfig(fn)

	// Build execution context
	funcExecCtx := &executor.ExecutionContext{
		RequestID:     execCtx.RequestID,
		TenantID:      execCtx.TenantID,
		UserID:        execCtx.UserID,
		CorrelationID: execCtx.CorrelationID,
		TraceID:       execCtx.TraceID,
	}

	// Execute the tool
	result, err := h.registry.Execute(ctx, funcExecCtx, config, &executor.ToolCall{
		ID:        toolCall.ID,
		Name:      toolCall.Function.Name,
		Arguments: args,
	})

	if err != nil || !result.Success {
		errMsg := "execution failed"
		if result != nil && result.Error != "" {
			errMsg = result.Error
		} else if err != nil {
			errMsg = err.Error()
		}
		errorType := "execution_error"
		if err != nil {
			errorType = "executor_error"
		}
		h.recordFunctionExecutionAndUsage(
			ctx,
			execCtx,
			fn,
			toolCall,
			args,
			result,
			errorType,
			errMsg,
			startedAt,
		)
		return errorResult(errMsg), err
	}

	// Format result content
	content := executor.FormatToolResult(result)
	result.Content = content
	h.recordFunctionExecutionAndUsage(
		ctx,
		execCtx,
		fn,
		toolCall,
		args,
		result,
		"",
		"",
		startedAt,
	)

	return ToolResultMessage{
		Role:       "tool",
		ToolCallID: toolCall.ID,
		Content:    content,
	}, nil
}

func (h *Handler) recordFunctionExecutionAndUsage(
	ctx context.Context,
	execCtx *ExecutionContext,
	fn *functionsquery.FunctionReadModel,
	toolCall ToolCallMessage,
	args map[string]interface{},
	result *executor.ToolResult,
	errorType, errorMessage string,
	startedAt time.Time,
) {
	if h.db == nil || fn == nil {
		return
	}

	completedAt := time.Now().UTC()
	durationMs := completedAt.Sub(startedAt).Milliseconds()
	success := errorMessage == ""

	outputPreview := ""
	if result != nil {
		if result.DurationMs > 0 {
			durationMs = result.DurationMs
		}
		success = success && result.Success
		if !success && errorMessage == "" {
			errorMessage = result.Error
		}
		if !success && errorType == "" {
			errorType = "execution_error"
		}
		outputPreview = truncatePreview(executor.FormatToolResult(result), 4000)
	}

	inputPreview := truncateJSONPreview(args, 2000)
	requestID := strings.TrimSpace(execCtx.RequestID)
	if requestID == "" {
		requestID = uuid.New().String()
	}

	const execInsert = `
		INSERT INTO function_executions (
			function_id, request_id, tenant_id, mode, tool_call_id,
			started_at, completed_at, duration_ms, success,
			error_type, error_message, input_preview, output_preview
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9,
			$10, $11, $12, $13
			)
	`
	if execTenantID, ok := chooseTenantUUID(execCtx.TenantID, fn.TenantID); ok {
		if _, err := h.db.ExecContext(ctx, execInsert,
			fn.ID, requestID, execTenantID, fn.Mode, toolCall.ID,
			startedAt, completedAt, durationMs, success,
			nullableString(errorType), nullableString(errorMessage), inputPreview, outputPreview,
		); err != nil {
			logger.WithFields(
				"function_id", fn.ID,
				"tool_call_id", toolCall.ID,
				"error", err.Error(),
			).Warn("toolloop: failed to persist function execution record")
		}
	} else {
		logger.WithFields(
			"function_id", fn.ID,
			"tool_call_id", toolCall.ID,
			"tenant_id_ctx", execCtx.TenantID,
			"tenant_id_fn", fn.TenantID,
		).Warn("toolloop: skipped function execution record due to missing UUID tenant")
	}

	tenantID := fallbackString(execCtx.TenantID, fn.TenantID)
	sourceRef := requestID + ":" + toolCall.ID
	periodStart := startedAt
	periodEnd := completedAt
	metadata := map[string]interface{}{
		"request_id":     requestID,
		"tool_call_id":   toolCall.ID,
		"function_name":  fn.Name,
		"mode":           fn.Mode,
		"success":        success,
		"duration_ms":    durationMs,
		"error_type":     errorType,
		"error_message":  errorMessage,
		"correlation_id": execCtx.CorrelationID,
	}
	invocationUsage := usage.BillingUsageRecord{
		IdempotencyKey: "function-invocation:" + sourceRef,
		TenantID:       tenantID,
		ResourceType:   "function",
		ResourceID:     fn.ID,
		SourceType:     "function.execution",
		SourceRef:      sourceRef,
		MetricType:     "function.invocations",
		Quantity:       1,
		Unit:           "count",
		CostUSD:        0,
		Metadata:       metadata,
		PeriodStart:    &periodStart,
		PeriodEnd:      &periodEnd,
	}
	durationUsage := usage.BillingUsageRecord{
		IdempotencyKey: "function-duration:" + sourceRef,
		TenantID:       tenantID,
		ResourceType:   "function",
		ResourceID:     fn.ID,
		SourceType:     "function.execution",
		SourceRef:      sourceRef,
		MetricType:     "function.duration_ms",
		Quantity:       float64(durationMs),
		Unit:           "ms",
		CostUSD:        0,
		Metadata:       metadata,
		PeriodStart:    &periodStart,
		PeriodEnd:      &periodEnd,
	}

	h.emitBillingUsageCommand(ctx, execCtx, invocationUsage)
	h.emitBillingUsageCommand(ctx, execCtx, durationUsage)
	if err := usage.InsertBillingUsageRecord(ctx, h.db, invocationUsage); err != nil {
		logger.WithFields(
			"function_id", fn.ID,
			"tool_call_id", toolCall.ID,
			"error", err.Error(),
		).Warn("toolloop: failed to persist function billing invocation record")
	}
	if err := usage.InsertBillingUsageRecord(ctx, h.db, durationUsage); err != nil {
		logger.WithFields(
			"function_id", fn.ID,
			"tool_call_id", toolCall.ID,
			"error", err.Error(),
		).Warn("toolloop: failed to persist function billing duration record")
	}
}

func (h *Handler) emitBillingUsageCommand(ctx context.Context, execCtx *ExecutionContext, rec usage.BillingUsageRecord) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil || sys == nil || sys.CommandBus == nil {
		return
	}

	cmd := usagecmd.NewRecordBillingUsageCommand(rec, execCtx.UserID, execCtx.TraceID)
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		logger.WithFields(
			"idempotency_key", rec.IdempotencyKey,
			"source_type", rec.SourceType,
			"source_ref", rec.SourceRef,
			"error", err.Error(),
		).Warn("toolloop: failed to dispatch billing usage command")
	}
}

func truncateJSONPreview(v interface{}, max int) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return truncatePreview(string(b), max)
}

func truncatePreview(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max]
}

func nullableString(s string) interface{} {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func fallbackString(primary, secondary string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	if strings.TrimSpace(secondary) != "" {
		return secondary
	}
	return "default"
}

func chooseTenantUUID(primary, secondary string) (string, bool) {
	p := strings.TrimSpace(primary)
	if _, err := uuid.Parse(p); err == nil {
		return p, true
	}
	s := strings.TrimSpace(secondary)
	if _, err := uuid.Parse(s); err == nil {
		return s, true
	}
	return "", false
}

// buildFunctionConfig converts a FunctionReadModel to an executor.FunctionConfig.
func (h *Handler) buildFunctionConfig(fn *functionsquery.FunctionReadModel) *executor.FunctionConfig {
	config := &executor.FunctionConfig{
		ID:         fn.ID,
		TenantID:   fn.TenantID,
		Name:       fn.Name,
		Mode:       executor.ExecutionMode(fn.Mode),
		TimeoutMs:  fn.TimeoutMs,
		MaxRetries: fn.MaxRetries,
	}

	// Parse parameters JSON
	if len(fn.Parameters) > 0 {
		json.Unmarshal(fn.Parameters, &config.Parameters)
	}

	// Webhook config
	if fn.WebhookURL.Valid {
		config.WebhookURL = fn.WebhookURL.String
	}
	if fn.WebhookMethod.Valid {
		config.WebhookMethod = fn.WebhookMethod.String
	}
	if fn.WebhookTimeoutMs.Valid {
		config.WebhookTimeoutMs = fn.WebhookTimeoutMs.Int32
	}
	if len(fn.WebhookHeaders) > 0 {
		json.Unmarshal(fn.WebhookHeaders, &config.WebhookHeaders)
	}

	// Proxy config
	if fn.ProxyBaseURL.Valid {
		config.ProxyBaseURL = fn.ProxyBaseURL.String
	}
	if fn.ProxyPath.Valid {
		config.ProxyPath = fn.ProxyPath.String
	}
	if fn.ProxyMethod.Valid {
		config.ProxyMethod = fn.ProxyMethod.String
	}
	if len(fn.ProxyQueryMapping) > 0 {
		json.Unmarshal(fn.ProxyQueryMapping, &config.ProxyQueryMapping)
	}
	if len(fn.ProxyHeaderMapping) > 0 {
		json.Unmarshal(fn.ProxyHeaderMapping, &config.ProxyHeaderMapping)
	}
	if len(fn.ProxyBodyMapping) > 0 {
		json.Unmarshal(fn.ProxyBodyMapping, &config.ProxyBodyMapping)
	}
	if len(fn.ProxyResponseMapping) > 0 {
		json.Unmarshal(fn.ProxyResponseMapping, &config.ProxyResponseMapping)
	}

	// Isolated config
	if fn.Runtime.Valid {
		config.Runtime = fn.Runtime.String
	}
	if fn.Code.Valid {
		config.Code = fn.Code.String
	}
	config.Packages = fn.Packages
	if fn.NetworkMode.Valid {
		config.NetworkMode = fn.NetworkMode.String
	} else {
		config.NetworkMode = "deny" // Default to no network access
	}
	config.AllowedHosts = fn.AllowedHosts
	config.VCPUs = int(fn.VCPUs)
	if config.VCPUs == 0 {
		config.VCPUs = 1
	}
	config.MemoryMB = int(fn.MemoryMB)
	if config.MemoryMB == 0 {
		config.MemoryMB = 512
	}
	if fn.DockerHost.Valid {
		config.DockerHost = fn.DockerHost.String
	}

	return config
}

// HasToolCalls checks if the response contains tool calls.
func HasToolCalls(finishReason string, toolCalls []ToolCallMessage) bool {
	return finishReason == "tool_calls" || finishReason == "tool_use" || len(toolCalls) > 0
}

// GenerateToolCallID generates a unique tool call ID.
func GenerateToolCallID() string {
	return "call_" + uuid.New().String()[:8]
}
