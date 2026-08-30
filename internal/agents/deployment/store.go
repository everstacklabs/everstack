package deployment

import "context"

// Store defines the persistence interface for agent deployments.
type Store interface {
	// Deployments
	CreateDeployment(ctx context.Context, d *Deployment) error
	GetDeployment(ctx context.Context, id, tenantID string) (*Deployment, error)
	GetActiveDeployment(ctx context.Context, agentID, tenantID string, version *int) (*Deployment, error)
	ListDeployments(ctx context.Context, agentID, tenantID string, limit, offset int) ([]*Deployment, int, error)
	UpdateDeployment(ctx context.Context, d *Deployment) error

	// Keys
	CreateKey(ctx context.Context, key *DeploymentKey) error
	GetKeyByHash(ctx context.Context, hash string) (*DeploymentKey, error)
	ListKeys(ctx context.Context, deploymentID string) ([]*DeploymentKey, error)
	RevokeKey(ctx context.Context, keyID string) error
	TouchKeyLastUsed(ctx context.Context, keyID string) error

	// Invocations
	RecordInvocation(ctx context.Context, inv *Invocation) error
	CompleteInvocation(ctx context.Context, id string, sessionID string, status string, output string, turns, promptTokens, completionTokens, durationMs int) error
	ListInvocations(ctx context.Context, deploymentID string, limit, offset int) ([]*Invocation, int, error)
}
