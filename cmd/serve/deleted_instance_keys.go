package serve

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/lib/tenantlive"
)

// Instance deletion runs in the services pod against the platform database,
// but API keys live in the gateway database, so deletion never revoked them
// and a deleted instance's keys kept authenticating. The managed gateway sees
// both databases, so it closes the gap twice:
//
//   - tenantlive rejects a key that resolves to a deleted instance at auth
//     time (immediately, including cached keys);
//   - this sweep revokes those keys in the gateway database, so they stay
//     dead even if the platform database is later unreachable.

// deletedInstanceSiteTakedown is the hosting server's TakeDownTenantSites,
// installed when evs.run hosting is enabled on this gateway.
var deletedInstanceSiteTakedown atomic.Pointer[func(context.Context, string) (int, error)]

// setDeletedInstanceSiteTakedown wires hosted-site takedown into the sweep.
func setDeletedInstanceSiteTakedown(fn func(context.Context, string) (int, error)) {
	if fn == nil {
		deletedInstanceSiteTakedown.Store(nil)
		return
	}
	deletedInstanceSiteTakedown.Store(&fn)
}

// takeDownDeletedInstanceSites unpublishes the hosted sites of each deleted
// instance. Deleting an instance frees its HOSTED_SITES quota, so its sites
// must stop serving at the edge. A failure is reported and retried on the
// next sweep; until then the site stays deleting and keeps counting.
func takeDownDeletedInstanceSites(ctx context.Context, deleted []string, takedown func(context.Context, string) (int, error)) (int, error) {
	if takedown == nil {
		return 0, nil
	}
	var (
		total int
		errs  error
	)
	for _, id := range deleted {
		n, err := takedown(ctx, id)
		total += n
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("instance %s: %w", id, err))
		}
	}
	return total, errs
}

// revokeDeletedInstanceKeys revokes every live key scoped to a deleted managed
// instance, by instance_id or by the org_id slot managed keys also use.
// Idempotent; returns how many keys it revoked.
func revokeDeletedInstanceKeys(ctx context.Context, platformDB, gatewayDB *sqlx.DB) (int64, error) {
	var deleted []string
	if err := platformDB.SelectContext(ctx, &deleted,
		`SELECT id::text FROM everstack.managed_instances WHERE deleted_at IS NOT NULL`); err != nil {
		return 0, fmt.Errorf("list deleted instances: %w", err)
	}
	if len(deleted) == 0 {
		return 0, nil
	}
	res, err := gatewayDB.ExecContext(ctx, `
		UPDATE api_keys
		SET revoked = TRUE, revoked_at = NOW(), updated_at = NOW()
		WHERE NOT COALESCE(revoked, FALSE)
		  AND (instance_id = ANY($1::text[]) OR org_id = ANY($1::text[]))`,
		pq.Array(deleted))
	if err != nil {
		return 0, fmt.Errorf("revoke deleted instance keys: %w", err)
	}
	return res.RowsAffected()
}

// startDeletedInstanceKeyGuard installs the auth-time check and runs the
// revoke sweep every interval until ctx ends. Managed gateways only.
func startDeletedInstanceKeyGuard(ctx context.Context, platformDB, gatewayDB *sqlx.DB, interval time.Duration) {
	if platformDB == nil || gatewayDB == nil {
		return
	}
	deleted := tenantlive.NewDeletedInstances(platformDB, 30*time.Second)
	refreshCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	if err := deleted.Refresh(refreshCtx); err != nil {
		logger.WithError(err).Warn("tenantlive: initial load of deleted instances failed; retrying in the background")
	}
	cancel()
	tenantlive.SetDeletedCheck(deleted.Deleted)
	tenantlive.SetDeletedLister(deleted.List)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			sweepCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			n, err := revokeDeletedInstanceKeys(sweepCtx, platformDB, gatewayDB)
			cancel()
			switch {
			case err != nil:
				logger.WithError(err).Warn("api keys: deleted-instance revoke sweep failed")
			case n > 0:
				logger.WithFields("revoked", n).Info("api keys: revoked keys of deleted instances")
			}
			if fn := deletedInstanceSiteTakedown.Load(); fn != nil {
				siteCtx, cancelSites := context.WithTimeout(ctx, 2*time.Minute)
				sites, err := takeDownDeletedInstanceSites(siteCtx, deleted.List(), *fn)
				cancelSites()
				if err != nil {
					logger.WithError(err).Warn("hosting: takedown of deleted instances' sites failed; retrying next sweep")
				}
				if sites > 0 {
					logger.WithFields("sites", sites).Info("hosting: took down sites of deleted instances")
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
