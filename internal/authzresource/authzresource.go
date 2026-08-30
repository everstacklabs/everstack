// Package authzresource records authorization-graph tuples for resource
// lifecycle events (create/delete) so the ReBAC engine can resolve inherited
// per-resource access. The tuple store is wired once at startup (from the same
// place the enforcement engine is built); until then, or when authz is disabled,
// every recorder is a no-op, so callers can invoke them unconditionally.
package authzresource

import (
	"context"
	"sync"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/pkg/authz"
)

var (
	mu    sync.RWMutex
	store authz.TupleStore
)

// SetStore wires the tuple store used to record resource lifecycle tuples. A nil
// store (the default) makes every recorder a no-op.
func SetStore(s authz.TupleStore) {
	mu.Lock()
	store = s
	mu.Unlock()
}

func current() authz.TupleStore {
	mu.RLock()
	defer mu.RUnlock()
	return store
}

// OnResourceCreated records the tuples for a newly-created resource:
//   - parent link tying it to its instance (the tenant), so the engine resolves
//     inherited access from the caller's session role (see the BridgeStore);
//   - a manager grant for the creator, so they retain full control (including
//     delete) of their own resource even as a plain member.
//
// Best-effort: failures are logged, never returned, so authorization bookkeeping
// cannot block resource creation. creatorUserID may be empty (no manager grant).
func OnResourceCreated(ctx context.Context, resourceType, resourceID, creatorUserID string) {
	s := current()
	if s == nil || resourceType == "" || resourceID == "" {
		return
	}
	tenant := contextkeys.GetTenantID(ctx)
	if tenant == "" {
		return
	}
	tuples := []authz.Tuple{authz.ResourceParent(resourceType, resourceID, tenant)}
	if creatorUserID != "" {
		tuples = append(tuples, authz.ResourceGrant(resourceType, resourceID, creatorUserID, "manager"))
	}
	wctx := authz.ContextWithTenant(ctx, tenant)
	if err := s.Write(wctx, tuples...); err != nil {
		logger.Warnf("authz: failed to record resource tuples for %s:%s: %v", resourceType, resourceID, err)
	}
}

// OnResourceDeleted removes ALL of a deleted resource's tuples — the parent link
// and every grant (creator manager grant, shares) — so nothing is left orphaned
// for a reused id to inherit. Best-effort, same as OnResourceCreated.
func OnResourceDeleted(ctx context.Context, resourceType, resourceID string) {
	s := current()
	if s == nil || resourceType == "" || resourceID == "" {
		return
	}
	tenant := contextkeys.GetTenantID(ctx)
	if tenant == "" {
		return
	}
	wctx := authz.ContextWithTenant(ctx, tenant)
	if err := s.DeleteObject(wctx, authz.Resource(resourceType, resourceID)); err != nil {
		logger.Warnf("authz: failed to remove resource tuples for %s:%s: %v", resourceType, resourceID, err)
	}
}
