package logger

import "encoding/json"

// LogPayload represents the structured payload for operational logs
// This provides rich nested context for dashboard queries and analytics
type LogPayload struct {
	Service     map[string]interface{} `json:"service,omitempty"`
	Deployment  map[string]interface{} `json:"deployment,omitempty"`
	Tenant      map[string]interface{} `json:"tenant,omitempty"`
	User        map[string]interface{} `json:"user,omitempty"`
	Request     map[string]interface{} `json:"request,omitempty"`
	Provider    map[string]interface{} `json:"provider,omitempty"`
	Prompt      map[string]interface{} `json:"prompt,omitempty"`
	Command     map[string]interface{} `json:"command,omitempty"`
	Correlation map[string]interface{} `json:"correlation,omitempty"`
	RateLimit   map[string]interface{} `json:"rate_limit,omitempty"`
	System      map[string]interface{} `json:"system,omitempty"`
	Logging     map[string]interface{} `json:"logging,omitempty"`
	Function    map[string]interface{} `json:"function,omitempty"`
	Workflow    map[string]interface{} `json:"workflow,omitempty"`
}

// PayloadBuilder helps construct structured payloads using a fluent API
type PayloadBuilder struct {
	payload LogPayload
}

// NewPayload creates a new payload builder
func NewPayload() *PayloadBuilder {
	return &PayloadBuilder{payload: LogPayload{}}
}

// WithService adds service context (name, version)
func (pb *PayloadBuilder) WithService(name, version string) *PayloadBuilder {
	pb.payload.Service = map[string]interface{}{
		"service.name":    name,
		"service.version": version,
	}
	return pb
}

// WithDeployment adds deployment context (mode, owner)
func (pb *PayloadBuilder) WithDeployment(mode, owner string) *PayloadBuilder {
	pb.payload.Deployment = map[string]interface{}{
		"deployment.mode": mode,
		"instance.owner":  owner,
	}
	return pb
}

// WithTenant adds tenant context (id, type, trooper)
func (pb *PayloadBuilder) WithTenant(tenantID, tenantType, trooperID string) *PayloadBuilder {
	pb.payload.Tenant = map[string]interface{}{
		"tenant.id":    tenantID,
		"tenant.type":  tenantType,
		"trooper.id": trooperID,
	}
	return pb
}

// WithUser adds user context (user_id, org_id)
func (pb *PayloadBuilder) WithUser(userID, orgID string) *PayloadBuilder {
	pb.payload.User = map[string]interface{}{
		"gateway.user.id": userID,
		"gateway.org.id":  orgID,
	}
	return pb
}

// WithRequest adds request context (id, endpoint, method, latency, etc.)
func (pb *PayloadBuilder) WithRequest(id, endpoint, method string, latencyMs int64) *PayloadBuilder {
	pb.payload.Request = map[string]interface{}{
		"gateway.request.id":         id,
		"gateway.request.endpoint":   endpoint,
		"gateway.request.method":     method,
		"gateway.request.latency_ms": latencyMs,
	}
	return pb
}

// WithRequestPhase adds the request phase to existing request context
func (pb *PayloadBuilder) WithRequestPhase(phase string) *PayloadBuilder {
	if pb.payload.Request == nil {
		pb.payload.Request = make(map[string]interface{})
	}
	pb.payload.Request["gateway.request.phase"] = phase
	return pb
}

// WithRequestInput adds user input to the request context
func (pb *PayloadBuilder) WithRequestInput(userInput string) *PayloadBuilder {
	if pb.payload.Request == nil {
		pb.payload.Request = make(map[string]interface{})
	}
	pb.payload.Request["user_input"] = userInput
	return pb
}

// WithProvider adds provider context (name, model, endpoint, status, latency)
func (pb *PayloadBuilder) WithProvider(name, model, endpoint string, statusCode int, latencyMs int64) *PayloadBuilder {
	pb.payload.Provider = map[string]interface{}{
		"gateway.provider.name":        name,
		"gateway.provider.model":       model,
		"gateway.provider.endpoint":    endpoint,
		"gateway.provider.status_code": statusCode,
		"gateway.provider.latency_ms":  latencyMs,
	}
	return pb
}

// WithProviderRateLimit adds rate limit info to provider context
func (pb *PayloadBuilder) WithProviderRateLimit(isLimited bool, retryAfterMs int64) *PayloadBuilder {
	if pb.payload.Provider == nil {
		pb.payload.Provider = make(map[string]interface{})
	}
	pb.payload.Provider["gateway.provider.rate_limited"] = isLimited
	pb.payload.Provider["gateway.provider.retry_after_ms"] = retryAfterMs
	return pb
}

// WithPrompt adds prompt/token context (input_tokens, output_tokens, completion_id)
func (pb *PayloadBuilder) WithPrompt(inputTokens, outputTokens int64, completionID string) *PayloadBuilder {
	pb.payload.Prompt = map[string]interface{}{
		"gateway.prompt.input_tokens":  inputTokens,
		"gateway.prompt.output_tokens": outputTokens,
		"gateway.prompt.completion_id": completionID,
		"gateway.prompt.truncated":     false,
	}
	return pb
}

// WithCommand adds command context (id, type, phase, elapsed_ms)
func (pb *PayloadBuilder) WithCommand(id, cmdType, phase string, elapsedMs int64) *PayloadBuilder {
	pb.payload.Command = map[string]interface{}{
		"gateway.command.id":         id,
		"gateway.command.type":       cmdType,
		"gateway.command.phase":      phase,
		"gateway.command.elapsed_ms": elapsedMs,
	}
	return pb
}

// WithCorrelation adds correlation/tracing context (correlation_id, trace_id, span_id)
func (pb *PayloadBuilder) WithCorrelation(correlationID string) *PayloadBuilder {
	pb.payload.Correlation = map[string]interface{}{
		"correlation.id": correlationID,
	}
	return pb
}

// WithTracing adds tracing IDs to correlation context
func (pb *PayloadBuilder) WithTracing(traceID, spanID string) *PayloadBuilder {
	if pb.payload.Correlation == nil {
		pb.payload.Correlation = make(map[string]interface{})
	}
	pb.payload.Correlation["trace.id"] = traceID
	pb.payload.Correlation["span.id"] = spanID
	return pb
}

// WithRateLimit adds rate limit context (remaining requests/tokens, is_limited)
func (pb *PayloadBuilder) WithRateLimit(remainingRequests, remainingTokens int64, isLimited bool) *PayloadBuilder {
	pb.payload.RateLimit = map[string]interface{}{
		"gateway.ratelimit.remaining_requests": remainingRequests,
		"gateway.ratelimit.remaining_tokens":   remainingTokens,
		"gateway.ratelimit.is_rate_limited":    isLimited,
	}
	return pb
}

// WithSystem adds system metrics (memory, CPU, goroutines)
func (pb *PayloadBuilder) WithSystem(memoryMB int64, cpuPct float64, goroutines int) *PayloadBuilder {
	pb.payload.System = map[string]interface{}{
		"gateway.system.memory_mb":  memoryMB,
		"gateway.system.cpu_pct":    cpuPct,
		"gateway.system.goroutines": goroutines,
	}
	return pb
}

// WithLogging adds logging metadata (category, phase, severity, caller, message)
func (pb *PayloadBuilder) WithLogging(category, phase, severity, caller, message string) *PayloadBuilder {
	pb.payload.Logging = map[string]interface{}{
		"log.category": category,
		"log.phase":    phase,
		"log.severity": severity,
		"log.caller":   caller,
		"log.message":  message,
	}
	return pb
}

// WithFunction adds function execution context
func (pb *PayloadBuilder) WithFunction(functionID, functionName, runtime, backend, executionMode string, durationMs int64) *PayloadBuilder {
	pb.payload.Function = map[string]interface{}{
		"function.id":             functionID,
		"function.name":           functionName,
		"function.runtime":        runtime,
		"function.backend":        backend,
		"function.execution_mode": executionMode,
		"function.duration_ms":    durationMs,
	}
	return pb
}

// WithFunctionOutput adds function stdout/stderr (truncated to 2KB each)
func (pb *PayloadBuilder) WithFunctionOutput(stdout, stderr string) *PayloadBuilder {
	if pb.payload.Function == nil {
		pb.payload.Function = make(map[string]interface{})
	}

	// Truncate to 2KB to avoid oversized logs
	const maxOutputLen = 2048
	if len(stdout) > maxOutputLen {
		stdout = stdout[:maxOutputLen] + "... (truncated)"
	}
	if len(stderr) > maxOutputLen {
		stderr = stderr[:maxOutputLen] + "... (truncated)"
	}

	pb.payload.Function["function.stdout"] = stdout
	pb.payload.Function["function.stderr"] = stderr
	return pb
}

// WithFunctionError adds function error details
func (pb *PayloadBuilder) WithFunctionError(errorType, errorMsg string) *PayloadBuilder {
	if pb.payload.Function == nil {
		pb.payload.Function = make(map[string]interface{})
	}
	pb.payload.Function["function.error_type"] = errorType
	pb.payload.Function["function.error"] = errorMsg
	return pb
}

// WithFunctionSuccess adds function success status and result summary
func (pb *PayloadBuilder) WithFunctionSuccess(success bool) *PayloadBuilder {
	if pb.payload.Function == nil {
		pb.payload.Function = make(map[string]interface{})
	}
	pb.payload.Function["function.success"] = success
	return pb
}

// WithWorkflowExecution adds workflow execution context
func (pb *PayloadBuilder) WithWorkflowExecution(executionID, workflowID, correlationID, tenantID string) *PayloadBuilder {
	pb.payload.Workflow = map[string]interface{}{
		"workflow.execution_id": executionID,
		"workflow.id":           workflowID,
		"workflow.correlation":  correlationID,
		"workflow.tenant_id":    tenantID,
	}
	return pb
}

// WithWorkflowNode adds per-node execution details to workflow context
func (pb *PayloadBuilder) WithWorkflowNode(nodeID, nodeType, nodeLabel string, durationMs int64, data map[string]string) *PayloadBuilder {
	if pb.payload.Workflow == nil {
		pb.payload.Workflow = make(map[string]interface{})
	}
	pb.payload.Workflow["workflow.node.id"] = nodeID
	pb.payload.Workflow["workflow.node.type"] = nodeType
	pb.payload.Workflow["workflow.node.label"] = nodeLabel
	pb.payload.Workflow["workflow.node.duration_ms"] = durationMs
	for k, v := range data {
		pb.payload.Workflow["workflow.node.data."+k] = v
	}
	return pb
}

// Build serializes the payload to a JSON string
// Returns empty string if serialization fails
func (pb *PayloadBuilder) Build() string {
	// Remove empty sections to keep payload compact
	if len(pb.payload.Service) == 0 {
		pb.payload.Service = nil
	}
	if len(pb.payload.Deployment) == 0 {
		pb.payload.Deployment = nil
	}
	if len(pb.payload.Tenant) == 0 {
		pb.payload.Tenant = nil
	}
	if len(pb.payload.User) == 0 {
		pb.payload.User = nil
	}
	if len(pb.payload.Request) == 0 {
		pb.payload.Request = nil
	}
	if len(pb.payload.Provider) == 0 {
		pb.payload.Provider = nil
	}
	if len(pb.payload.Prompt) == 0 {
		pb.payload.Prompt = nil
	}
	if len(pb.payload.Command) == 0 {
		pb.payload.Command = nil
	}
	if len(pb.payload.Correlation) == 0 {
		pb.payload.Correlation = nil
	}
	if len(pb.payload.RateLimit) == 0 {
		pb.payload.RateLimit = nil
	}
	if len(pb.payload.System) == 0 {
		pb.payload.System = nil
	}
	if len(pb.payload.Logging) == 0 {
		pb.payload.Logging = nil
	}
	if len(pb.payload.Function) == 0 {
		pb.payload.Function = nil
	}
	if len(pb.payload.Workflow) == 0 {
		pb.payload.Workflow = nil
	}

	data, err := json.Marshal(pb.payload)
	if err != nil {
		return ""
	}
	return string(data)
}
