package v1

import (
	"context"
	"errors"
	"fmt"

	"github.com/everstacklabs/everstack/internal/hosting/moderation"
)

// ModerationManifestEnforcer returns the authoritative serving-plane
// projection. It rewrites the current R2 manifest with the database's desired
// status and generation; the sites Worker compares this generation with KV.
func (s *Server) ModerationManifestEnforcer() moderation.EdgeEnforcer {
	return moderation.EdgeEnforcerFunc(s.applyModerationManifest)
}

func (s *Server) withModerationProjectionLock(
	ctx context.Context,
	slug string,
	fn func(context.Context) error,
) error {
	return moderation.NewPostgresStore(s.db).WithProjectionLock(ctx, slug, fn)
}

func (s *Server) applyModerationManifest(ctx context.Context, action moderation.Action) error {
	if s == nil || s.db == nil || s.store == nil {
		return errors.New("hosting manifest projection is not configured")
	}
	site, err := s.loadSiteBySlug(ctx, action.Slug)
	if err != nil {
		return fmt.Errorf("load site for moderation: %w", err)
	}
	if site.ID != action.SiteID || site.ModerationGeneration != action.Generation {
		return errors.New("moderation action is no longer the site's desired generation")
	}
	if site.Status == "deleted" {
		return errors.New("site was deleted before moderation projection")
	}
	// A site can be moderated before its first publish is finalized. There is
	// no manifest to rewrite yet; activation will carry the desired status and
	// generation when it creates one.
	if site.CurrentVersion == nil {
		return nil
	}
	if err := s.rewriteManifest(ctx, site); err != nil {
		return fmt.Errorf("rewrite moderation manifest: %w", err)
	}
	if err := s.purger.PurgeSlug(ctx, site.Slug); err != nil {
		return fmt.Errorf("purge moderated site cache: %w", err)
	}
	return nil
}
