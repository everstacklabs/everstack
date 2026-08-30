package catalog_sync

import (
	"context"
	"time"

	"github.com/everstacklabs/everstack/internal/domain/provider_config"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// FreshnessJob manages model freshness updates (new -> stable transitions)
type FreshnessJob struct {
	modelRepo       *provider_config.ModelRepository
	freshnessWindow time.Duration // Default: 8 weeks
	checkInterval   time.Duration // How often to check (default: 24 hours)
}

// NewFreshnessJob creates a new freshness update job
func NewFreshnessJob(modelRepo *provider_config.ModelRepository) *FreshnessJob {
	return &FreshnessJob{
		modelRepo:       modelRepo,
		freshnessWindow: 8 * 7 * 24 * time.Hour, // 8 weeks
		checkInterval:   24 * time.Hour,         // Check daily
	}
}

// Start starts the freshness update job
func (j *FreshnessJob) Start(ctx context.Context) {
	// Run immediately on start
	if err := j.updateFreshness(ctx); err != nil {
		logger.Warnf("catalog_sync: initial freshness update failed: %v", err)
	}

	// Start background ticker
	ticker := time.NewTicker(j.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := j.updateFreshness(ctx); err != nil {
				logger.Warnf("catalog_sync: freshness update failed: %v", err)
			}
		}
	}
}

// updateFreshness updates model freshness based on age
func (j *FreshnessJob) updateFreshness(ctx context.Context) error {
	if err := j.modelRepo.UpdateFreshness(ctx, j.freshnessWindow); err != nil {
		return err
	}

	return nil
}
