// Package agentrun provides a small synchronous "run an Everstack agent and
// get its final text" helper, shared by the inbound MCP server (run_agent
// tool) and the A2A server. Both expose Everstack agents to external callers,
// and both need the same thing: given (tenant, agentID, message), invoke the
// agent's active deployment and return the assistant's output.
//
// Only *deployed* agents are runnable this way. That is deliberate: a
// deployment is a frozen, rate-limited, tenant-scoped config snapshot — the
// right unit to expose to the outside world. Undeployed drafts are not callable
// externally.
package agentrun

import (
	"context"
	"fmt"

	"github.com/everstacklabs/everstack/internal/agents/deployment"
)

// Runner invokes deployed agents synchronously.
type Runner struct {
	inv   *deployment.Invoker
	store deployment.Store
}

// New constructs a Runner from the deployment invoker and store.
func New(inv *deployment.Invoker, store deployment.Store) *Runner {
	return &Runner{inv: inv, store: store}
}

// Run invokes the tenant's active deployment of agentID with message and
// returns the agent's final text output. Tenant scoping is enforced by
// GetActiveDeployment (its query filters by tenant_id), so a caller can only
// ever reach its own tenant's agents.
func (r *Runner) Run(ctx context.Context, tenantID, agentID, message string) (string, error) {
	if r == nil || r.inv == nil || r.store == nil {
		return "", fmt.Errorf("agent runner is not configured")
	}
	if tenantID == "" || agentID == "" {
		return "", fmt.Errorf("tenant id and agent id are required")
	}

	dep, err := r.store.GetActiveDeployment(ctx, agentID, tenantID, nil)
	if err != nil || dep == nil {
		return "", fmt.Errorf("agent %q has no active deployment for this tenant; deploy it to call it via MCP/A2A", agentID)
	}

	resp, err := r.inv.InvokeSync(ctx, dep, &deployment.InvokeRequest{Message: message})
	if err != nil {
		return "", fmt.Errorf("agent invocation failed: %w", err)
	}
	if resp == nil {
		return "", fmt.Errorf("agent invocation returned no response")
	}
	if resp.Status != "completed" {
		detail := resp.Error
		if detail == "" {
			detail = resp.Status
		}
		// Surface any partial output alongside the failure for context.
		if resp.Output != "" {
			return resp.Output, fmt.Errorf("agent run did not complete (%s)", detail)
		}
		return "", fmt.Errorf("agent run did not complete (%s)", detail)
	}
	return resp.Output, nil
}
