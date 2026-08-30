// Package isolation provides isolated function execution backends.
package isolation

import (
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/sirupsen/logrus"
)

// ExecutionLogger provides structured logging for function execution events.
// It's designed to be backend-agnostic and works with both Docker and Firecracker.
type ExecutionLogger struct {
	backendName string
}

// NewExecutionLogger creates a new execution logger for the specified backend.
func NewExecutionLogger(backendName string) *ExecutionLogger {
	return &ExecutionLogger{
		backendName: backendName,
	}
}

// LogExecutionStarted logs the start of a function execution.
func (l *ExecutionLogger) LogExecutionStarted(req ExecutionRequest, warm bool) {
	executionMode := "cold"
	if warm {
		executionMode = "warm"
	}

	payload := logger.NewPayload().
		WithCorrelation(req.CorrelationID).
		WithTracing(req.TraceID, "").
		WithTenant(req.TenantID, "", "").
		WithFunction(req.FunctionID, req.FunctionName, string(req.Runtime), l.backendName, executionMode, 0).
		Build()

	logger.WithCategory(logger.CategoryOperational).
		WithLogEvent(logger.EventFunctionExecutionStarted).
		WithPayload(payload).
		WithFields(logrus.Fields{
			"function_id":    req.FunctionID,
			"function_name":  req.FunctionName,
			"runtime":        req.Runtime,
			"backend":        l.backendName,
			"execution_mode": executionMode,
			"correlation_id": req.CorrelationID,
		}).
		Info("function execution started")
}

// LogExecutionCompleted logs the successful completion of a function execution.
func (l *ExecutionLogger) LogExecutionCompleted(req ExecutionRequest, result *ExecutionResult, warm bool) {
	executionMode := "cold"
	if warm {
		executionMode = "warm"
	}

	payload := logger.NewPayload().
		WithCorrelation(req.CorrelationID).
		WithTracing(req.TraceID, "").
		WithTenant(req.TenantID, "", "").
		WithFunction(req.FunctionID, req.FunctionName, string(req.Runtime), l.backendName, executionMode, result.DurationMS).
		WithFunctionOutput(result.Stdout, result.Stderr).
		WithFunctionSuccess(result.Success).
		Build()

	logger.WithCategory(logger.CategoryOperational).
		WithLogEvent(logger.EventFunctionExecutionCompleted).
		WithPayload(payload).
		WithFields(logrus.Fields{
			"function_id":    req.FunctionID,
			"function_name":  req.FunctionName,
			"runtime":        req.Runtime,
			"backend":        l.backendName,
			"execution_mode": executionMode,
			"duration_ms":    result.DurationMS,
			"success":        result.Success,
			"correlation_id": req.CorrelationID,
		}).
		Info("function execution completed")
}

// LogExecutionError logs a function execution error.
func (l *ExecutionLogger) LogExecutionError(req ExecutionRequest, result *ExecutionResult, warm bool, err error) {
	executionMode := "cold"
	if warm {
		executionMode = "warm"
	}

	var durationMs int64
	var errorType, errorMsg, stdout, stderr string

	if result != nil {
		durationMs = result.DurationMS
		errorType = string(result.ErrorType)
		errorMsg = result.Error
		stdout = result.Stdout
		stderr = result.Stderr
	}

	if err != nil && errorMsg == "" {
		errorMsg = err.Error()
	}

	payload := logger.NewPayload().
		WithCorrelation(req.CorrelationID).
		WithTracing(req.TraceID, "").
		WithTenant(req.TenantID, "", "").
		WithFunction(req.FunctionID, req.FunctionName, string(req.Runtime), l.backendName, executionMode, durationMs).
		WithFunctionOutput(stdout, stderr).
		WithFunctionError(errorType, errorMsg).
		WithFunctionSuccess(false).
		Build()

	logger.WithCategory(logger.CategoryOperational).
		WithLogEvent(logger.EventFunctionExecutionError).
		WithPayload(payload).
		WithFields(logrus.Fields{
			"function_id":    req.FunctionID,
			"function_name":  req.FunctionName,
			"runtime":        req.Runtime,
			"backend":        l.backendName,
			"execution_mode": executionMode,
			"duration_ms":    durationMs,
			"error_type":     errorType,
			"error":          errorMsg,
			"correlation_id": req.CorrelationID,
		}).
		Error("function execution error")
}
