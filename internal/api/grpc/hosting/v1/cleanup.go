package v1

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/hosting"
)

const (
	pendingPublishTTL  = time.Hour
	pendingReaperLimit = 100
)

type stalePendingVersion struct {
	ID      string `db:"id"`
	Slug    string `db:"slug"`
	Version int32  `db:"version"`
}

// ReconcileDeletingSites completes durable deletions that previously stopped
// after Postgres recorded intent. Ordering by the oldest attempt, while the
// delete path refreshes updated_at, rotates a repeatedly failing site behind
// untouched work instead of letting it starve the queue.
func (s *Server) ReconcileDeletingSites(ctx context.Context, limit int) (int, error) {
	if s == nil || s.db == nil || s.store == nil {
		return 0, errors.New("hosting is not configured")
	}
	if limit <= 0 || limit > pendingReaperLimit {
		limit = pendingReaperLimit
	}

	var candidates []siteRow
	if err := s.db.SelectContext(ctx, &candidates, `
		SELECT `+siteColumns+`
		FROM sites
		WHERE status = 'deleting'
		ORDER BY updated_at ASC
		LIMIT $1`, limit); err != nil {
		return 0, fmt.Errorf("list deleting sites: %w", err)
	}

	completed := 0
	var reconcileErr error
	for i := range candidates {
		candidate := &candidates[i]
		err := s.withModerationProjectionLock(ctx, candidate.Slug, func(lockCtx context.Context) error {
			current, err := s.loadSiteBySlug(lockCtx, candidate.Slug)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return fmt.Errorf("reload deleting site: %w", err)
			}
			if current.Status != "deleting" {
				return nil
			}
			return s.deleteOwnedSite(lockCtx, current)
		})
		if err != nil {
			reconcileErr = errors.Join(reconcileErr, fmt.Errorf("%s: %w", candidate.Slug, err))
			continue
		}
		completed++
	}
	return completed, reconcileErr
}

// ReapStalePending removes uploads whose one-hour completion window elapsed.
// It also releases finalized versions retained by a deleted site until every
// presigned upload URL was guaranteed to expire. A storage deletion failure
// keeps the reservation for retry instead of letting R2 usage escape the cap.
func (s *Server) ReapStalePending(ctx context.Context, limit int) (int, error) {
	if s == nil || s.db == nil || s.store == nil {
		return 0, errors.New("hosting is not configured")
	}
	if limit <= 0 || limit > pendingReaperLimit {
		limit = pendingReaperLimit
	}

	var candidates []stalePendingVersion
	if err := s.db.SelectContext(ctx, &candidates, `
		SELECT v.id, s.slug, v.version
		FROM site_versions v
		JOIN sites s ON s.id = v.site_id
		WHERE (
			v.status = 'pending'
			OR (s.status = 'deleted' AND v.status = 'finalized')
		) AND v.created_at < $1
		ORDER BY v.cleanup_attempted_at ASC NULLS FIRST, v.created_at ASC
		LIMIT $2`, time.Now().UTC().Add(-pendingPublishTTL), limit); err != nil {
		return 0, fmt.Errorf("list stale pending publishes: %w", err)
	}

	cleaned := 0
	var cleanupErr error
	for _, candidate := range candidates {
		if err := s.abandonPendingVersion(ctx, candidate.Slug, candidate.Version); err != nil {
			if markErr := s.markCleanupAttempt(ctx, candidate.ID); markErr != nil {
				err = errors.Join(err, fmt.Errorf("record cleanup attempt: %w", markErr))
			}
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("%s v%d: %w", candidate.Slug, candidate.Version, err))
			continue
		}
		cleaned++
	}
	return cleaned, cleanupErr
}

func (s *Server) markCleanupAttempt(ctx context.Context, versionID string) error {
	markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := s.db.ExecContext(markCtx,
		`UPDATE site_versions SET cleanup_attempted_at = NOW() WHERE id = $1`, versionID,
	)
	return err
}

// abandonPendingVersion serializes cleanup with finalize, claim, settings,
// delete, and moderation so it cannot remove a version while it becomes live.
func (s *Server) abandonPendingVersion(ctx context.Context, slug string, version int32) error {
	return s.withModerationProjectionLock(ctx, slug, func(lockCtx context.Context) error {
		return s.abandonPendingVersionLocked(lockCtx, slug, version)
	})
}

func (s *Server) abandonPendingVersionLocked(ctx context.Context, slug string, version int32) error {
	site, err := s.loadSiteBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("load pending site: %w", err)
	}

	var versionID, status string
	if err := s.db.QueryRowContext(ctx, `
		SELECT id, status
		FROM site_versions
		WHERE site_id = $1 AND version = $2`, site.ID, version).Scan(&versionID, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("load pending version: %w", err)
	}
	pending := status == "pending"
	deletedFinalized := site.Status == "deleted" && status == "finalized"
	if !pending && !deletedFinalized {
		return nil
	}

	if err := s.repairSiteProjection(ctx, site); err != nil {
		return err
	}

	var objectKeys []string
	if err := s.db.SelectContext(ctx, &objectKeys,
		`SELECT r2_key FROM site_files WHERE version_id = $1 ORDER BY r2_key`, versionID,
	); err != nil {
		return fmt.Errorf("list pending objects: %w", err)
	}
	for _, key := range objectKeys {
		if err := s.store.Delete(ctx, s.cfg.Bucket, key); err != nil {
			return fmt.Errorf("delete pending object %q: %w", key, err)
		}
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin pending cleanup: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if deletedFinalized {
		result, err := tx.ExecContext(ctx,
			`UPDATE site_versions SET status = 'deleted' WHERE id = $1 AND status = 'finalized'`, versionID,
		)
		if err != nil {
			return fmt.Errorf("release deleted finalized version: %w", err)
		}
		if _, err := result.RowsAffected(); err != nil {
			return fmt.Errorf("verify deleted finalized-version cleanup: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit deleted finalized-version cleanup: %w", err)
		}
		return nil
	}

	result, err := tx.ExecContext(ctx,
		`DELETE FROM site_versions WHERE id = $1 AND status = 'pending'`, versionID,
	)
	if err != nil {
		return fmt.Errorf("delete pending version: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("verify pending version cleanup: %w", err)
	} else if changed != 1 {
		return nil
	}

	// A never-live site with no moderation history has no externally visible
	// generation to retain, so free its slug and site slot. If moderation did
	// touch it, retain a tombstone and close pending actions instead.
	deleted, err := tx.ExecContext(ctx, `
		DELETE FROM sites s
		WHERE s.id = $1
			AND s.status NOT IN ('deleted', 'deleting')
			AND s.current_version IS NULL
			AND NOT EXISTS (SELECT 1 FROM site_versions v WHERE v.site_id = s.id)
			AND NOT EXISTS (SELECT 1 FROM site_moderation_actions a WHERE a.site_id = s.id)`, site.ID)
	if err != nil {
		return fmt.Errorf("delete empty pending site: %w", err)
	}
	if rows, err := deleted.RowsAffected(); err != nil {
		return fmt.Errorf("verify empty site cleanup: %w", err)
	} else if rows == 0 {
		tombstoned, err := tx.ExecContext(ctx, `
			UPDATE sites
			SET status = 'deleted', manifest_key = NULL, claim_token_hash = NULL, updated_at = NOW()
			WHERE id = $1
				AND status NOT IN ('deleted', 'deleting')
				AND current_version IS NULL
				AND NOT EXISTS (SELECT 1 FROM site_versions v WHERE v.site_id = sites.id)`, site.ID)
		if err != nil {
			return fmt.Errorf("tombstone empty pending site: %w", err)
		}
		if rows, err := tombstoned.RowsAffected(); err != nil {
			return fmt.Errorf("verify empty site tombstone: %w", err)
		} else if rows > 0 {
			if _, err := tx.ExecContext(ctx, `
				UPDATE site_moderation_actions
				SET status = 'superseded',
					last_error = 'superseded because the unfinished site expired',
					lease_token = NULL,
					lease_expires_at = NULL,
					updated_at = NOW()
				WHERE site_id = $1 AND status = 'pending'`, site.ID); err != nil {
				return fmt.Errorf("close unfinished site moderation actions: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pending cleanup: %w", err)
	}

	return nil
}

func (s *Server) cleanupFailedPublish(ctx context.Context, slug string, version int32) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return s.abandonPendingVersion(cleanupCtx, slug, version)
}

// repairFailedActivation restores the edge pointer after activation fails but
// deliberately leaves the pending version and objects reserved. Presigned
// upload URLs have already been returned to the caller and remain valid for
// 15 minutes, so only the one-hour reaper may safely release that storage.
func (s *Server) repairFailedActivation(ctx context.Context, slug string) error {
	recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return s.withModerationProjectionLock(recoveryCtx, slug, func(lockCtx context.Context) error {
		site, err := s.loadSiteBySlug(lockCtx, slug)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("load site after failed activation: %w", err)
			}
			site = &siteRow{Slug: slug, Status: "deleted"}
		}
		return s.repairSiteProjection(lockCtx, site)
	})
}

// repairSiteProjection must run under the per-slug projection lock. It
// restores the DB-authoritative manifest (or removes an unfinished/deleted
// one) and purges immediately, independent of later object cleanup success.
func (s *Server) repairSiteProjection(ctx context.Context, site *siteRow) error {
	var projectionErr error
	if site.CurrentVersion != nil && site.Status != "deleted" && site.Status != "deleting" {
		if err := s.rewriteManifest(ctx, site); err != nil {
			projectionErr = fmt.Errorf("restore authoritative site manifest: %w", err)
		}
	} else if err := s.store.Delete(ctx, s.cfg.Bucket, hosting.ManifestKey(site.Slug)); err != nil {
		projectionErr = fmt.Errorf("remove unfinished site manifest: %w", err)
	}
	var purgeErr error
	if s.purger != nil {
		if err := s.purger.PurgeSlug(ctx, site.Slug); err != nil {
			purgeErr = fmt.Errorf("purge repaired site manifest: %w", err)
		}
	}
	return errors.Join(projectionErr, purgeErr)
}
