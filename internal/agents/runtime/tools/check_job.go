package tools

import (
	"context"
	"fmt"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// CheckJobHandler handles the check_job synthetic tool call.
type CheckJobHandler struct {
	JobQueue agentrt.JobQueue
}

// Name returns the tool name.
func (h *CheckJobHandler) Name() string { return "check_job" }

// Definition returns the tool definition.
func (h *CheckJobHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "check_job",
			Description: "Check the status of a background job that was spawned asynchronously. Returns the current status and result if completed.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"job_id": map[string]interface{}{
						"type":        "string",
						"description": "The ID of the job to check.",
					},
				},
				"required": []string{"job_id"},
			},
		},
	}
}

// Execute checks the status of an async job.
func (h *CheckJobHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	jobID, _ := args["job_id"].(string)
	if jobID == "" {
		return "", fmt.Errorf("job_id is required")
	}

	if h.JobQueue == nil {
		return "Async job queue is not available.", nil
	}

	status, detail, err := h.JobQueue.Status(ctx, jobID)
	if err != nil {
		return fmt.Sprintf("Job not found: %s", err.Error()), nil
	}

	switch status {
	case agentrt.JobStatusPending:
		return fmt.Sprintf("Job %s is pending (waiting for an execution slot).", jobID), nil
	case agentrt.JobStatusRunning:
		return fmt.Sprintf("Job %s is still running.", jobID), nil
	case agentrt.JobStatusCompleted:
		if detail != "" {
			return fmt.Sprintf("Job %s completed.\nResult: %s", jobID, detail), nil
		}
		return fmt.Sprintf("Job %s completed (no output).", jobID), nil
	case agentrt.JobStatusFailed:
		return fmt.Sprintf("Job %s failed: %s", jobID, detail), nil
	case agentrt.JobStatusCancelled:
		return fmt.Sprintf("Job %s was cancelled.", jobID), nil
	default:
		return fmt.Sprintf("Job %s has unknown status: %s", jobID, status), nil
	}
}
