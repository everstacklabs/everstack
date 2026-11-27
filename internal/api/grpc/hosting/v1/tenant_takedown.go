package v1

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// TakeDownTenantSites unpublishes every site a tenant (a deleted managed
// instance) still has, through the normal site deletion path: the site turns
// deleting, the manifest the edge Worker serves is removed, objects are
// cleaned up, and only then is it tombstoned.
//
// Deleting an instance frees its HOSTED_SITES quota, so its sites must stop
// serving, or an org could publish, delete the instance and publish again
// without limit. Idempotent: deleted sites are skipped. A site whose takedown
// fails stays in the durable deleting state, which the hosting quota keeps
// counting, and the next call retries it. Returns how many sites were fully
// taken down.
//
// tenantID must be server-known (the deleted-instance sweep), never taken
// from a request.
func (s *Server) TakeDownTenantSites(ctx context.Context, tenantID string) (int, error) {
	tenantID = strings.TrimSpace(tenantID)
	if s == nil || s.db == nil || s.store == nil || tenantID == "" {
		return 0, nil
	}
	var sites []siteRow
	if err := s.db.SelectContext(ctx, &sites, `
		SELECT `+siteColumns+`
		FROM sites
		WHERE tenant_id::text = $1 AND status <> 'deleted'
		ORDER BY created_at, id`, tenantID); err != nil {
		return 0, fmt.Errorf("list sites of deleted instance: %w", err)
	}
	var (
		done int
		errs error
	)
	for i := range sites {
		site := &sites[i]
		if err := s.withModerationProjectionLock(ctx, site.Slug, func(lockCtx context.Context) error {
			return s.deleteOwnedSite(lockCtx, site)
		}); err != nil {
			errs = errors.Join(errs, fmt.Errorf("take down site %s: %w", site.Slug, err))
			continue
		}
		done++
	}
	return done, errs
}
