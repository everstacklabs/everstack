package config_sync

import (
	"context"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/domain/provider_config"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Worker handles background synchronization of provider configurations to YAML
type Worker struct {
	repo           *provider_config.Repository
	configPath     string
	syncQueue      chan struct{}
	debounceWindow time.Duration
	mu             sync.Mutex
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
}

// NewWorker creates a new sync worker
func NewWorker(repo *provider_config.Repository, configPath string) *Worker {
	ctx, cancel := context.WithCancel(context.Background())

	return &Worker{
		repo:           repo,
		configPath:     configPath,
		syncQueue:      make(chan struct{}, 100),
		debounceWindow: 5 * time.Second,
		ctx:            ctx,
		cancel:         cancel,
	}
}

// Start begins the background sync worker
func (w *Worker) Start() {
	w.wg.Add(1)
	go w.run()
}

// Stop gracefully stops the worker
func (w *Worker) Stop() {
	w.cancel()
	w.wg.Wait()
}

// TriggerSync queues a sync operation (non-blocking)
func (w *Worker) TriggerSync() {
	select {
	case w.syncQueue <- struct{}{}:
		logger.Debugf("config_sync: sync triggered")
	default:
		logger.Debugf("config_sync: sync already queued")
	}
}

// run is the main worker loop
func (w *Worker) run() {
	defer w.wg.Done()

	var debounceTimer *time.Timer
	var pendingSync bool

	for {
		select {
		case <-w.ctx.Done():
			// Perform final sync if pending
			if pendingSync {
				w.performSync()
			}
			return

		case <-w.syncQueue:
			// Reset debounce timer
			if debounceTimer != nil {
				debounceTimer.Stop()
			}

			pendingSync = true
			debounceTimer = time.AfterFunc(w.debounceWindow, func() {
				w.mu.Lock()
				defer w.mu.Unlock()

				if pendingSync {
					w.performSync()
					pendingSync = false
				}
			})
		}
	}
}

// performSync executes the actual sync operation
func (w *Worker) performSync() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Get all active configurations from database
	configs, err := w.repo.List(ctx)
	if err != nil {
		return
	}

	// Write to YAML
	if err := SyncToYAML(w.configPath, configs); err != nil {
		return
	}

	// Update sync times for all configs
	for _, config := range configs {
		if err := w.repo.UpdateSyncTime(ctx, config.ProviderName); err != nil {
			logger.Warnf("config_sync: failed to update sync time for %s: %v", config.ProviderName, err)
		}
	}
}

// SyncNow performs an immediate sync (blocking)
func (w *Worker) SyncNow() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Get all configurations
	configs, err := w.repo.List(ctx)
	if err != nil {
		return err
	}

	// Write to YAML
	if err := SyncToYAML(w.configPath, configs); err != nil {
		return err
	}

	// Update sync times
	for _, config := range configs {
		if err := w.repo.UpdateSyncTime(ctx, config.ProviderName); err != nil {
			logger.Warnf("config_sync: failed to update sync time for %s: %v", config.ProviderName, err)
		}
	}

	return nil
}
