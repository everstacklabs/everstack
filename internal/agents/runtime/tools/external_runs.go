package tools

import (
	"context"
	"errors"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// ExternalRuns is bound by the host to the authenticated tenant. No tool
// argument can supply a tenant, endpoint, token or upstream task identifier.
type ExternalRuns interface {
	Start(context.Context, string, string, string) (string, error)
	Read(context.Context, string, bool, bool) (string, error)
}

func externalRunDefinition(name, description string, properties map[string]interface{}, required []string) gw.ToolDefinition {
	return gw.ToolDefinition{Type: "function", Function: gw.ToolFunctionDef{Name: name, Description: description, Parameters: map[string]interface{}{"type": "object", "properties": properties, "required": required, "additionalProperties": false}}}
}
func externalString(description string) map[string]interface{} {
	return map[string]interface{}{"type": "string", "description": description}
}
func durableCallDefinition() gw.ToolDefinition {
	return externalRunDefinition("call_external_agent", "Start work on a saved external agent. Returns a durable run ID and observed state, which may still be working or require input. Only state completed is a completed answer. Use the same idempotency_key when retrying the same request; never resend work with an unknown outcome. Use get_external_agent_run to check progress.", map[string]interface{}{
		"remote": externalString("Name of a saved remote agent."), "message": externalString("Work for the remote agent."), "idempotency_key": externalString("Unique key of 8–128 characters for this work. Reuse exactly this key and message on retry."),
	}, []string{"remote", "message", "idempotency_key"})
}

type ExternalRunHandler struct {
	Runs   ExternalRuns
	Cancel bool
}

func (h *ExternalRunHandler) Name() string {
	if h.Cancel {
		return "cancel_external_agent_run"
	}
	return "get_external_agent_run"
}
func (h *ExternalRunHandler) Definition() gw.ToolDefinition {
	description := "Read a saved external-agent run and refresh its status using its existing task handle. Does not start work again. Input/auth-required states need intervention; continuation is not yet supported."
	if h.Cancel {
		description = "Request cancellation of an external-agent run. Cancellation is only confirmed when the returned state is canceled; it may race with completion. This does not undo actions already taken."
	}
	return externalRunDefinition(h.Name(), description, map[string]interface{}{"run_id": externalString("Everstack run ID returned by call_external_agent, not the remote task ID.")}, []string{"run_id"})
}
func (h *ExternalRunHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	id, _ := args["run_id"].(string)
	if id == "" || h.Runs == nil {
		return "", errors.New("a saved run_id is required")
	}
	return h.Runs.Read(ctx, id, true, h.Cancel)
}
