package v1

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/everstacklabs/everstack/internal/hosting"
)

// PublishDirect implements hosting.Publisher for in-process callers (the
// agent runtime's publish_site tool). It runs the same validation, slug
// resolution and activation as the public API, but writes objects straight
// to storage instead of round-tripping presigned URLs. Tenant is required:
// agents always run tenant-scoped, so this path never creates anonymous
// sites.
func (s *Server) PublishDirect(ctx context.Context, tenantID, slug string, spaFallback bool, files []hosting.DirectFile) (publishedURL string, returnErr error) {
	if err := s.ready(); err != nil {
		return "", err
	}
	if tenantID == "" {
		return "", errors.New("tenant context required")
	}
	if len(files) == 0 {
		return "", errors.New("at least one file is required")
	}
	if len(files) > maxFiles {
		return "", fmt.Errorf("too many files (max %d)", maxFiles)
	}

	entries := make([]manifestFile, 0, len(files))
	var total int64
	seen := make(map[string]struct{}, len(files))
	for _, f := range files {
		p, err := normalizeSitePath(f.Path)
		if err != nil {
			return "", err
		}
		if _, dup := seen[p]; dup {
			return "", fmt.Errorf("duplicate path %q", p)
		}
		seen[p] = struct{}{}
		size := int64(len(f.Content))
		if size == 0 {
			return "", fmt.Errorf("empty content for %q", p)
		}
		if size > maxFileBytes {
			return "", fmt.Errorf("%q exceeds the per-file limit", p)
		}
		total += size
		if total > maxTotalBytes {
			return "", errors.New("site exceeds the total size limit")
		}
		sum := sha256.Sum256(f.Content)
		entries = append(entries, manifestFile{
			path:        p,
			sizeBytes:   size,
			contentType: contentTypeFor(p, f.ContentType),
			sha256:      hex.EncodeToString(sum[:]),
		})
	}
	if len(entries) == 0 {
		return "", errors.New("at least one file is required")
	}

	token, err := hosting.NewToken()
	if err != nil {
		return "", err
	}
	resolvedSlug, version, err := s.resolveAndRegister(ctx, tenantID, "", slug, "public", spaFallback, entries, total, hashToken(token))
	if err != nil {
		return "", err
	}
	completed := false
	defer func() {
		if completed || returnErr == nil {
			return
		}
		if cleanupErr := s.cleanupFailedPublish(ctx, resolvedSlug, version); cleanupErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("release failed publish reservation: %w", cleanupErr))
		}
	}()

	// Keep the same per-slug projection lock used by the stale-publish reaper
	// for the complete direct upload. A direct publish can therefore never be
	// reaped mid-PUT and leave objects behind after its reservation is removed.
	if err := s.withModerationProjectionLock(ctx, resolvedSlug, func(lockCtx context.Context) error {
		rows := make([]siteFileRow, 0, len(entries))
		for i, e := range entries {
			key := hosting.ObjectKey(resolvedSlug, version, e.path)
			if _, err := s.store.Put(lockCtx, s.cfg.Bucket, key, e.contentType, bytes.NewReader(files[i].Content)); err != nil {
				return fmt.Errorf("failed to upload %s: %w", e.path, err)
			}
			sum := e.sha256
			rows = append(rows, siteFileRow{
				Path:        e.path,
				R2Key:       key,
				ContentType: e.contentType,
				SizeBytes:   e.sizeBytes,
				SHA256:      &sum,
			})
		}

		site, err := s.loadSiteBySlug(lockCtx, resolvedSlug)
		if err != nil {
			return fmt.Errorf("failed to reload site: %w", err)
		}
		var versionID string
		if err := s.db.QueryRowContext(lockCtx,
			`SELECT id FROM site_versions WHERE site_id = $1 AND version = $2`,
			site.ID, version,
		).Scan(&versionID); err != nil {
			return fmt.Errorf("failed to load version: %w", err)
		}

		_, _, err = s.activateVersionLocked(lockCtx, site, versionID, version, rows)
		return err
	}); err != nil {
		return "", err
	}
	completed = true
	return s.siteURL(resolvedSlug), nil
}

// ListSitesForTenant implements hosting.SiteLister for in-process callers
// (the MCP list_sites tool). Tenant-scoped; never returns anonymous sites.
func (s *Server) ListSitesForTenant(ctx context.Context, tenantID string) ([]hosting.SiteInfo, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if tenantID == "" {
		return nil, errors.New("tenant context required")
	}
	var rows []siteRow
	if err := s.db.SelectContext(ctx, &rows,
		`SELECT `+siteColumns+` FROM sites WHERE tenant_id = $1 AND status NOT IN ('deleted', 'deleting') ORDER BY created_at DESC LIMIT 200`,
		tenantID,
	); err != nil {
		return nil, err
	}
	out := make([]hosting.SiteInfo, 0, len(rows))
	for i := range rows {
		p := s.toProto(&rows[i])
		out = append(out, hosting.SiteInfo{
			Slug:       p.GetSlug(),
			URL:        p.GetUrl(),
			Status:     p.GetStatus().String(),
			FileCount:  p.GetFileCount(),
			TotalBytes: p.GetTotalBytes(),
		})
	}
	return out, nil
}
