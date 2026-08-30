package v1

import (
	"context"
	"fmt"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/compact"
)

// compactConfigFromFeatures translates the validator-package config
// into the compact package's Config. Kept in its own file so the
// gateway server doesn't accumulate one more bootstrap helper inside
// bootstrap_from_config.go.
//
// Defaults fill in any zero-valued fields so a partial YAML override
// still produces a viable config (e.g. only setting `enabled: true`
// gets the canonical 0.80/0.85/0.95 thresholds rather than zeros).
func compactConfigFromFeatures(f *validator.FeaturesConfig) compact.Config {
	out := compact.DefaultConfig()
	if f == nil {
		return out
	}
	c := f.Compact

	out.Enabled = c.Enabled
	if c.MaxContextTokens > 0 {
		out.MaxContextTokens = c.MaxContextTokens
	}
	if c.BackgroundThreshold > 0 && c.BackgroundThreshold < 1 {
		out.BackgroundThreshold = c.BackgroundThreshold
	}
	if c.AggressiveThreshold > 0 && c.AggressiveThreshold < 1 {
		out.AggressiveThreshold = c.AggressiveThreshold
	}
	if c.EmergencyThreshold > 0 && c.EmergencyThreshold <= 1 {
		out.EmergencyThreshold = c.EmergencyThreshold
	}
	if c.SummarizationModel != "" {
		out.SummarizationModel = c.SummarizationModel
	}
	if len(c.EnabledForProviders) > 0 {
		out.EnabledForProviders = append([]string(nil), c.EnabledForProviders...)
	}
	return out
}

// makeCompactSummarizerResolver returns the lazy resolver the
// compaction middleware uses to look up the summarisation model. It
// captures the Server so each call walks the live router (which is
// rebuilt on tenant config change). Returns ChatProvider, since the
// compactor's Summarizer only needs Chat.
//
// On any failure (nil server, no router for the request's tenant,
// model not registered) the resolver returns an error and the
// middleware falls through with the original request — see
// compact.Middleware.maybeCompact.
func (s *Server) makeCompactSummarizerResolver() compact.SummarizerResolver {
	return func(ctx context.Context, model string) (gw.ChatProvider, error) {
		if s == nil {
			return nil, fmt.Errorf("compact: server is nil")
		}
		router := s.routerFor(ctx)
		if router == nil {
			return nil, fmt.Errorf("compact: no provider router for ctx (tenant)")
		}
		provider, _, err := router.ResolveWithContext(ctx, model)
		if err != nil {
			return nil, fmt.Errorf("compact: resolve summarisation model %s: %w", model, err)
		}
		// Every provider built through the factory implements
		// ChatProvider since the underlying type satisfies all of
		// gw.Provider. The type assertion is just to narrow the
		// interface returned to compact.NewSummarizer.
		if cp, ok := provider.(gw.ChatProvider); ok {
			return cp, nil
		}
		return nil, fmt.Errorf("compact: resolved provider does not implement ChatProvider")
	}
}
