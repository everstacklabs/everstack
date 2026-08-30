package catalog_sync

import (
	"context"
	"time"

	"github.com/everstacklabs/everstack/internal/domain/provider_config"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// CleanupJob manages cleanup of deprecated providers and models
type CleanupJob struct {
	providerRepo   *provider_config.Repository
	modelRepo      *provider_config.ModelRepository
	deprecationAge time.Duration // How long to keep deprecated items (default: 90 days)
	checkInterval  time.Duration // How often to check (default: 24 hours)
}

// NewCleanupJob creates a new cleanup job
func NewCleanupJob(providerRepo *provider_config.Repository, modelRepo *provider_config.ModelRepository) *CleanupJob {
	return &CleanupJob{
		providerRepo:   providerRepo,
		modelRepo:      modelRepo,
		deprecationAge: 90 * 24 * time.Hour, // 90 days
		checkInterval:  24 * time.Hour,      // Check daily
	}
}

// Start starts the cleanup job
func (j *CleanupJob) Start(ctx context.Context) {
	// Don't run immediately on start - wait for first interval
	ticker := time.NewTicker(j.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := j.cleanup(ctx); err != nil {
				logger.Warnf("catalog_sync: cleanup failed: %v", err)
			}
		}
	}
}

// cleanup removes deprecated items older than the threshold
func (j *CleanupJob) cleanup(ctx context.Context) error {
	// For now, we just log - actual deletion will be implemented when needed
	// This is intentionally conservative to prevent accidental data loss

	// Future implementation:
	// 1. Query providers where deprecated_at < (now - deprecationAge)
	// 2. Query models where deprecated_at < (now - deprecationAge)
	// 3. Delete or archive them
	return nil
}
