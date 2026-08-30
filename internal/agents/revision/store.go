package revision

import (
	"context"
	"errors"
)

var (
	ErrNotFound      = errors.New("agent revision not found")
	ErrAgentNotFound = errors.New("agent not found")
)

// Store persists immutable revisions and resolves the revision pinned to an
// agent session.
type Store interface {
	CreateAndActivate(ctx context.Context, tenantID, agentID, createdBy string, manifest *Manifest) (*Revision, bool, error)
	Get(ctx context.Context, tenantID, revisionID string) (*Revision, error)
	GetActive(ctx context.Context, tenantID, agentID string) (*Revision, error)
	GetForSession(ctx context.Context, tenantID, agentID, sessionID string) (*Revision, error)
}
