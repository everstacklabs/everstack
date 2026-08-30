package everstack

import (
	"context"
	"fmt"
	"net/url"
)

// AgentsResource provides agent operations.
type AgentsResource struct {
	Sessions   *AgentSessionsResource
	Reviews    *AgentReviewsResource
	Sandboxes  *AgentSandboxesResource
	Lifecycle  *AgentLifecycleResource
	Memories   *AgentMemoriesResource
	Deployments *AgentDeploymentsResource
	Triggers   *AgentTriggersResource
	Links      *AgentLinksResource
	Channels   *AgentChannelsResource
	t          *Transport
}

func newAgentsResource(t *Transport) *AgentsResource {
	return &AgentsResource{
		Sessions:    &AgentSessionsResource{t: t},
		Reviews:     &AgentReviewsResource{t: t},
		Sandboxes:   &AgentSandboxesResource{t: t},
		Lifecycle:   &AgentLifecycleResource{t: t},
		Memories:    &AgentMemoriesResource{t: t},
		Deployments: &AgentDeploymentsResource{t: t},
		Triggers:    &AgentTriggersResource{t: t},
		Links:       &AgentLinksResource{t: t},
		Channels:    &AgentChannelsResource{t: t},
		t:           t,
	}
}

// Create creates an agent.
func (r *AgentsResource) Create(ctx context.Context, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/agents", body, nil, &resp)
}

// Get retrieves an agent by ID.
func (r *AgentsResource) Get(ctx context.Context, agentID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", fmt.Sprintf("/v1/agents/%s", agentID), nil, nil, &resp)
}

// List lists agents.
func (r *AgentsResource) List(ctx context.Context, params ...url.Values) (map[string]any, error) {
	var q url.Values
	if len(params) > 0 {
		q = params[0]
	}
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", "/v1/agents", nil, q, &resp)
}

// Update updates an agent.
func (r *AgentsResource) Update(ctx context.Context, agentID string, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "PATCH", fmt.Sprintf("/v1/agents/%s", agentID), body, nil, &resp)
}

// Delete deletes an agent.
func (r *AgentsResource) Delete(ctx context.Context, agentID string) error {
	return r.t.Request(ctx, "DELETE", fmt.Sprintf("/v1/agents/%s", agentID), nil, nil, nil)
}

// --- Sessions ---

// AgentSessionsResource provides agent session operations.
type AgentSessionsResource struct {
	t *Transport
}

// Create creates a new agent session.
func (r *AgentSessionsResource) Create(ctx context.Context, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/agents/sessions", body, nil, &resp)
}

// Get retrieves a session by ID.
func (r *AgentSessionsResource) Get(ctx context.Context, sessionID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", fmt.Sprintf("/v1/agents/sessions/%s", sessionID), nil, nil, &resp)
}

// List lists sessions.
func (r *AgentSessionsResource) List(ctx context.Context, params ...url.Values) (map[string]any, error) {
	var q url.Values
	if len(params) > 0 {
		q = params[0]
	}
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", "/v1/agents/sessions", nil, q, &resp)
}

// RunTurn runs a single turn in a session.
func (r *AgentSessionsResource) RunTurn(ctx context.Context, sessionID string, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", fmt.Sprintf("/v1/agents/sessions/%s/turns", sessionID), body, nil, &resp)
}

// RunTurnStream runs a streaming turn in a session.
func (r *AgentSessionsResource) RunTurnStream(ctx context.Context, sessionID string, body map[string]any) (*Stream[map[string]any], error) {
	return newStream[map[string]any](ctx, r.t, "POST", fmt.Sprintf("/v1/agents/sessions/%s/turns/stream", sessionID), body)
}

// Cancel cancels an active session.
func (r *AgentSessionsResource) Cancel(ctx context.Context, sessionID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", fmt.Sprintf("/v1/agents/sessions/%s/cancel", sessionID), map[string]any{}, nil, &resp)
}

// Complete marks a session as complete.
func (r *AgentSessionsResource) Complete(ctx context.Context, sessionID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", fmt.Sprintf("/v1/agents/sessions/%s/complete", sessionID), map[string]any{}, nil, &resp)
}

// Steer injects a message into a running session.
func (r *AgentSessionsResource) Steer(ctx context.Context, sessionID string, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", fmt.Sprintf("/v1/agents/sessions/%s/steer", sessionID), body, nil, &resp)
}

// --- Reviews ---

// AgentReviewsResource provides HITL review operations.
type AgentReviewsResource struct {
	t *Transport
}

// Submit submits a review decision.
func (r *AgentReviewsResource) Submit(ctx context.Context, reviewID string, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", fmt.Sprintf("/v1/agents/reviews/%s/submit", reviewID), body, nil, &resp)
}

// Get retrieves a review by ID.
func (r *AgentReviewsResource) Get(ctx context.Context, reviewID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", fmt.Sprintf("/v1/agents/reviews/%s", reviewID), nil, nil, &resp)
}

// List lists reviews.
func (r *AgentReviewsResource) List(ctx context.Context, params ...url.Values) (map[string]any, error) {
	var q url.Values
	if len(params) > 0 {
		q = params[0]
	}
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", "/v1/agents/reviews", nil, q, &resp)
}

// --- Sandboxes ---

// AgentSandboxesResource provides sandbox operations.
type AgentSandboxesResource struct {
	t *Transport
}

// Create creates a sandbox.
func (r *AgentSandboxesResource) Create(ctx context.Context, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/sandbox", body, nil, &resp)
}

// GetOverview returns the sandbox overview.
func (r *AgentSandboxesResource) GetOverview(ctx context.Context) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", "/v1/sandbox/overview", nil, nil, &resp)
}

// ListInstances lists sandbox instances.
func (r *AgentSandboxesResource) ListInstances(ctx context.Context, params ...url.Values) (map[string]any, error) {
	var q url.Values
	if len(params) > 0 {
		q = params[0]
	}
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", "/v1/sandbox/instances", nil, q, &resp)
}

// GetInstance retrieves a sandbox instance.
func (r *AgentSandboxesResource) GetInstance(ctx context.Context, sandboxID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", fmt.Sprintf("/v1/sandbox/instances/%s", sandboxID), nil, nil, &resp)
}

// Destroy destroys a sandbox.
func (r *AgentSandboxesResource) Destroy(ctx context.Context, sessionID string) error {
	return r.t.Request(ctx, "DELETE", fmt.Sprintf("/v1/sandbox/%s", sessionID), nil, nil, nil)
}

// Stop stops a sandbox.
func (r *AgentSandboxesResource) Stop(ctx context.Context, sandboxID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", fmt.Sprintf("/v1/sandbox/%s/stop", sandboxID), map[string]any{}, nil, &resp)
}

// Revive revives a stopped sandbox.
func (r *AgentSandboxesResource) Revive(ctx context.Context, sandboxID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", fmt.Sprintf("/v1/sandbox/%s/revive", sandboxID), map[string]any{}, nil, &resp)
}

// Terminate permanently terminates a sandbox.
func (r *AgentSandboxesResource) Terminate(ctx context.Context, sandboxID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", fmt.Sprintf("/v1/sandbox/%s/terminate", sandboxID), map[string]any{}, nil, &resp)
}

// --- Lifecycle ---

// AgentLifecycleResource provides persistent agent lifecycle operations.
type AgentLifecycleResource struct {
	t *Transport
}

// Provision provisions a persistent agent.
func (r *AgentLifecycleResource) Provision(ctx context.Context, agentID string, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", fmt.Sprintf("/v1/agents/%s/provision", agentID), body, nil, &resp)
}

// Sleep puts an agent to sleep.
func (r *AgentLifecycleResource) Sleep(ctx context.Context, agentID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", fmt.Sprintf("/v1/agents/%s/sleep", agentID), map[string]any{}, nil, &resp)
}

// Wake wakes a sleeping agent.
func (r *AgentLifecycleResource) Wake(ctx context.Context, agentID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", fmt.Sprintf("/v1/agents/%s/wake", agentID), map[string]any{}, nil, &resp)
}

// --- Memories ---

// AgentMemoriesResource provides agent memory operations.
type AgentMemoriesResource struct {
	t *Transport
}

// List lists memories for an agent.
func (r *AgentMemoriesResource) List(ctx context.Context, agentID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", fmt.Sprintf("/v1/agents/%s/memories", agentID), nil, nil, &resp)
}

// Create creates a memory for an agent.
func (r *AgentMemoriesResource) Create(ctx context.Context, agentID string, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", fmt.Sprintf("/v1/agents/%s/memories", agentID), body, nil, &resp)
}

// Update updates a memory.
func (r *AgentMemoriesResource) Update(ctx context.Context, memoryID string, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "PATCH", fmt.Sprintf("/v1/agents/memories/%s", memoryID), body, nil, &resp)
}

// Deactivate deactivates a memory.
func (r *AgentMemoriesResource) Deactivate(ctx context.Context, memoryID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", fmt.Sprintf("/v1/agents/memories/%s/deactivate", memoryID), map[string]any{}, nil, &resp)
}

// Delete deletes a memory.
func (r *AgentMemoriesResource) Delete(ctx context.Context, memoryID string) error {
	return r.t.Request(ctx, "DELETE", fmt.Sprintf("/v1/agents/memories/%s", memoryID), nil, nil, nil)
}

// --- Deployments ---

// AgentDeploymentsResource provides agent deployment operations.
type AgentDeploymentsResource struct {
	t *Transport
}

// Deploy deploys an agent.
func (r *AgentDeploymentsResource) Deploy(ctx context.Context, agentID string, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", fmt.Sprintf("/v1/agents/%s/deploy", agentID), body, nil, &resp)
}

// List lists deployments for an agent.
func (r *AgentDeploymentsResource) List(ctx context.Context, agentID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", fmt.Sprintf("/v1/agents/%s/deployments", agentID), nil, nil, &resp)
}

// Get retrieves a deployment by ID.
func (r *AgentDeploymentsResource) Get(ctx context.Context, deploymentID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", fmt.Sprintf("/v1/deployments/%s", deploymentID), nil, nil, &resp)
}

// Update updates a deployment.
func (r *AgentDeploymentsResource) Update(ctx context.Context, deploymentID string, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "PATCH", fmt.Sprintf("/v1/deployments/%s", deploymentID), body, nil, &resp)
}

// --- Triggers ---

// AgentTriggersResource provides agent trigger operations.
type AgentTriggersResource struct {
	t *Transport
}

// Create creates a trigger for an agent.
func (r *AgentTriggersResource) Create(ctx context.Context, agentID string, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", fmt.Sprintf("/v1/agents/%s/triggers", agentID), body, nil, &resp)
}

// List lists triggers for an agent.
func (r *AgentTriggersResource) List(ctx context.Context, agentID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", fmt.Sprintf("/v1/agents/%s/triggers", agentID), nil, nil, &resp)
}

// Get retrieves a trigger by ID.
func (r *AgentTriggersResource) Get(ctx context.Context, triggerID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", fmt.Sprintf("/v1/agent-triggers/%s", triggerID), nil, nil, &resp)
}

// Update updates a trigger.
func (r *AgentTriggersResource) Update(ctx context.Context, triggerID string, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "PATCH", fmt.Sprintf("/v1/agent-triggers/%s", triggerID), body, nil, &resp)
}

// Delete deletes a trigger.
func (r *AgentTriggersResource) Delete(ctx context.Context, triggerID string) error {
	return r.t.Request(ctx, "DELETE", fmt.Sprintf("/v1/agent-triggers/%s", triggerID), nil, nil, nil)
}

// --- Links ---

// AgentLinksResource provides agent link operations.
type AgentLinksResource struct {
	t *Transport
}

// Create creates a link between agents.
func (r *AgentLinksResource) Create(ctx context.Context, sourceAgentID string, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", fmt.Sprintf("/v1/agents/%s/links", sourceAgentID), body, nil, &resp)
}

// List lists links for an agent.
func (r *AgentLinksResource) List(ctx context.Context, agentID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", fmt.Sprintf("/v1/agents/%s/links", agentID), nil, nil, &resp)
}

// Delete deletes a link.
func (r *AgentLinksResource) Delete(ctx context.Context, linkID string) error {
	return r.t.Request(ctx, "DELETE", fmt.Sprintf("/v1/agent-links/%s", linkID), nil, nil, nil)
}

// --- Channels ---

// AgentChannelsResource provides channel binding operations.
type AgentChannelsResource struct {
	t *Transport
}

// Bind binds a channel to an agent.
func (r *AgentChannelsResource) Bind(ctx context.Context, agentID string, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", fmt.Sprintf("/v1/agents/%s/channels", agentID), body, nil, &resp)
}

// Unbind unbinds a channel from an agent.
func (r *AgentChannelsResource) Unbind(ctx context.Context, agentID, channelConfigID string) error {
	return r.t.Request(ctx, "DELETE", fmt.Sprintf("/v1/agents/%s/channels/%s", agentID, channelConfigID), nil, nil, nil)
}

// List lists channel bindings for an agent.
func (r *AgentChannelsResource) List(ctx context.Context, agentID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", fmt.Sprintf("/v1/agents/%s/channels", agentID), nil, nil, &resp)
}
