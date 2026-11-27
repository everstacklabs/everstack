// Package tenantlive answers whether a tenant id belongs to a deleted managed
// instance, for the API-key authentication paths on a managed gateway.
//
// Deleting an instance (services pod, platform DB) never touched its API
// keys, which live in the gateway DB, and key auth did not check that the
// instance was live, so a deleted instance's keys kept working. Key auth now
// rejects any key that resolves to a deleted instance. Self-hosted gateways
// install no check and are unaffected.
package tenantlive

import (
	"context"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

var deletedCheck atomic.Pointer[func(string) bool]

// SetDeletedCheck installs the managed-gateway check. Nil clears it.
func SetDeletedCheck(fn func(tenantID string) bool) {
	if fn == nil {
		deletedCheck.Store(nil)
		return
	}
	deletedCheck.Store(&fn)
}

// Deleted reports whether tenantID is a deleted managed instance. False when
// no check is installed.
func Deleted(tenantID string) bool {
	fn := deletedCheck.Load()
	if fn == nil || strings.TrimSpace(tenantID) == "" {
		return false
	}
	return (*fn)(tenantID)
}

var deletedLister atomic.Pointer[func() []string]

// SetDeletedLister installs the source of deleted instance ids for sweeps
// that stop their workloads. Nil clears it.
func SetDeletedLister(fn func() []string) {
	if fn == nil {
		deletedLister.Store(nil)
		return
	}
	deletedLister.Store(&fn)
}

// DeletedIDs lists the deleted managed instances known to this replica,
// sorted. Empty when no lister is installed (self-hosted).
func DeletedIDs() []string {
	fn := deletedLister.Load()
	if fn == nil {
		return nil
	}
	return (*fn)()
}

// DeletedInstances is a periodically refreshed set of deleted managed
// instance ids read from the platform database.
type DeletedInstances struct {
	db  *sqlx.DB
	ttl time.Duration

	mu       sync.RWMutex
	ids      map[string]struct{}
	loadedAt time.Time
	loading  atomic.Bool
}

// NewDeletedInstances builds the set; ttl bounds how long a deletion takes to
// reach key auth.
func NewDeletedInstances(db *sqlx.DB, ttl time.Duration) *DeletedInstances {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &DeletedInstances{db: db, ttl: ttl, ids: map[string]struct{}{}}
}

// Refresh reloads the set. On error the previous snapshot is kept: forgetting
// known deletions would re-admit the deleted instances' keys.
func (d *DeletedInstances) Refresh(ctx context.Context) error {
	var ids []string
	if err := d.db.SelectContext(ctx, &ids,
		`SELECT id::text FROM everstack.managed_instances WHERE deleted_at IS NOT NULL`); err != nil {
		return err
	}
	next := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		next[strings.ToLower(strings.TrimSpace(id))] = struct{}{}
	}
	d.mu.Lock()
	d.ids, d.loadedAt = next, time.Now()
	d.mu.Unlock()
	return nil
}

// Deleted reports whether tenantID is a deleted instance, refreshing in the
// background once the snapshot is older than the TTL.
func (d *DeletedInstances) Deleted(tenantID string) bool {
	d.mu.RLock()
	_, deleted := d.ids[strings.ToLower(strings.TrimSpace(tenantID))]
	stale := time.Since(d.loadedAt) > d.ttl
	d.mu.RUnlock()
	if stale && d.loading.CompareAndSwap(false, true) {
		go func() {
			defer d.loading.Store(false)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := d.Refresh(ctx); err != nil {
				logger.WithError(err).Warn("tenantlive: refresh of deleted instances failed; keeping the last snapshot")
			}
		}()
	}
	return deleted
}

// List returns the known deleted instance ids, sorted.
func (d *DeletedInstances) List() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]string, 0, len(d.ids))
	for id := range d.ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
