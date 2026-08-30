package catalog_sync

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/catalogdistribution"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Service manages catalog synchronization
type Service struct {
	config  *Config
	fetcher *Fetcher
	cache   *Cache
	merger  *Merger

	// State
	mu                sync.RWMutex
	syncMu            sync.Mutex
	isRunning         bool
	lastSync          time.Time
	currentVersion    string
	remoteVersion     string
	hasUpdates        bool
	newModelsCount    int
	newProvidersCount int
	newModelsList     []string // Actual list of new model names
	newProvidersList  []string // Actual list of new provider names
	updatedModelsList []string // List of updated models
	deprecatedList    []string // List of deprecated items
	pendingCatalog    *MergeResult

	// Embedded catalogs for fallback
	embeddedModels    *validator.ModelsConfig
	embeddedProviders map[string]interface{}

	// Merged catalogs (embedded + remote/local)
	mergedProviders map[string]interface{}

	// Database reconciler for syncing to DB
	dbReconciler CatalogDBReconciler

	// Catalog refresher for notifying provider catalog to reload
	catalogRefresher CatalogRefresher
}

// CatalogRefresher interface for notifying catalog updates
type CatalogRefresher interface {
	Refresh() error
}

// NewService creates a new catalog sync service
func NewService(config *Config, embeddedModels *validator.ModelsConfig, embeddedProviders map[string]interface{}) *Service {
	// Create fetcher based on source type
	var fetcher *Fetcher
	if config.Source == "local" {
		fetcher = NewLocalFetcher(config.LocalPath)
	} else {
		fetcher = NewFetcher(config.RemoteURL, config.Timeout, config.MaxRetries, config.RetryDelay, catalogdistribution.TrustConfig{
			Channel:          config.Channel,
			PublicKey:        config.PublicKey,
			PublicKeys:       config.PublicKeys,
			RequireSignature: config.RequireSignature,
		})
	}

	cache := NewCache(config.CacheDir)
	merger := NewMerger(embeddedModels, embeddedProviders)

	service := &Service{
		config:            config,
		fetcher:           fetcher,
		cache:             cache,
		merger:            merger,
		embeddedModels:    embeddedModels,
		embeddedProviders: embeddedProviders,
		mergedProviders:   embeddedProviders, // Initialize with embedded
	}
	service.restoreCachedState()
	return service
}

func (s *Service) restoreCachedState() {
	if !s.cache.HasCache() {
		return
	}

	files, err := s.cache.LoadCatalog()
	if err != nil {
		logger.Warnf("catalog_sync: failed to restore cached catalog: %v", err)
		return
	}
	merged, err := s.merger.MergeCatalogs(files)
	if err != nil {
		logger.Warnf("catalog_sync: failed to restore merged catalog state: %v", err)
		return
	}

	s.currentVersion = files.Version
	s.mergedProviders = merged.Providers
	if metadata, err := s.cache.GetMetadata(); err == nil {
		s.lastSync = metadata.LastSync
		s.newModelsCount = metadata.NewModels
		s.newProvidersCount = metadata.NewProviders
	}
}

// Start starts the catalog sync service
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isRunning {
		return fmt.Errorf("service is already running")
	}

	s.isRunning = true

	// Local development can load immediately without network access.
	if s.config.Source == "local" {
		go func() {
			if err := s.syncCatalog(ctx, false); err != nil {
				logger.Warnf("catalog_sync: initial local catalog sync failed: %v", err)
			}
		}()
	}
	if !s.config.EnableAutoSync {
		return nil
	}
	if s.config.SyncInterval <= 0 {
		s.isRunning = false
		return fmt.Errorf("catalog sync interval must be positive")
	}

	// Remote refresh has a short quiet window and never sits on the readiness
	// path. The loop performs its first update before starting the interval.
	go s.backgroundSync(ctx)
	return nil
}

// Stop stops the catalog sync service
func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.isRunning = false
}

// GetStatus returns current catalog status
func (s *Service) GetStatus() (string, string, bool, int, int, time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.currentVersion, s.remoteVersion, s.hasUpdates, s.newModelsCount, s.newProvidersCount, s.lastSync
}

// GetSyncSource returns the sync source (URL for remote, path for local)
func (s *Service) GetSyncSource() string {
	if s.config.Source == "local" {
		return s.config.LocalPath
	}
	return s.config.RemoteURL
}

// IsAutoSyncEnabled returns whether auto-sync is enabled
func (s *Service) IsAutoSyncEnabled() bool {
	return s.config.EnableAutoSync
}

// TriggerSync triggers a manual catalog sync
func (s *Service) TriggerSync(ctx context.Context) error {
	logger.Debug("catalog_sync: manual sync triggered")
	err := s.syncCatalog(ctx, true)
	if err != nil {
		logger.Errorf("catalog_sync: manual sync failed: %v", err)
		return err
	}
	logger.Debug("catalog_sync: manual sync completed successfully")
	return nil
}

// GetCachedCatalog returns the cached catalog
func (s *Service) GetCachedCatalog() (*validator.ModelsConfig, error) {
	s.mu.RLock()
	if s.pendingCatalog != nil {
		models := s.pendingCatalog.Models
		s.mu.RUnlock()
		return models, nil
	}
	s.mu.RUnlock()

	if !s.cache.HasCache() {
		return s.embeddedModels, nil
	}

	files, err := s.cache.LoadCatalog()
	if err != nil {
		return s.embeddedModels, err
	}

	// Parse cached models
	models, err := validator.ParseModelsDefaults(files.Models)
	if err != nil {
		return s.embeddedModels, err
	}

	return models, nil
}

// GetCachedProviders returns the merged providers (embedded + remote/local)
func (s *Service) GetCachedProviders() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.pendingCatalog != nil {
		return s.pendingCatalog.Providers
	}

	// Return merged providers (will be embedded on first load, then merged after sync)
	return s.mergedProviders
}

// GetChangelog returns the versioned changelog stored with the applied catalog.
func (s *Service) GetChangelog() (*Changelog, error) {
	if !s.cache.HasCache() {
		return &Changelog{}, nil
	}

	data, err := s.cache.LoadChangelog()
	if err != nil {
		return nil, err
	}
	return ParseChangelog(data)
}

// GetNewModels returns the list of new models from last sync
func (s *Service) GetNewModels() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.newModelsList
}

// GetNewProviders returns the list of new providers from last sync
func (s *Service) GetNewProviders() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.newProvidersList
}

// GetUpdatedModels returns the list of updated models from last sync
func (s *Service) GetUpdatedModels() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.updatedModelsList
}

// GetDeprecatedItems returns the list of deprecated items from last sync
func (s *Service) GetDeprecatedItems() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deprecatedList
}

// backgroundSync runs the background sync loop
func (s *Service) backgroundSync(ctx context.Context) {
	initialDelay := 30 * time.Second
	if s.config.SyncInterval < initialDelay {
		initialDelay = s.config.SyncInterval
	}
	initialSync := time.NewTimer(initialDelay)
	defer initialSync.Stop()
	select {
	case <-ctx.Done():
		return
	case <-initialSync.C:
		if !s.running() {
			return
		}
		if err := s.syncCatalog(ctx, false); err != nil {
			logger.Warnf("catalog_sync: initial catalog sync failed: %v", err)
		}
	}

	ticker := time.NewTicker(s.config.SyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !s.running() {
				return
			}
			if s.config.EnableAutoSync {
				if err := s.syncCatalog(ctx, false); err != nil {
					logger.Warnf("catalog_sync: background catalog sync failed: %v", err)
				}
			}
		}
	}
}

func (s *Service) running() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isRunning
}

// syncCatalog performs a catalog sync
func (s *Service) syncCatalog(ctx context.Context, force bool) error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	// Drain the active bundle's durable projection journal before touching the
	// distribution endpoint. Database/event recovery must keep working when R2
	// is unavailable, using only the atomically cached last-known-good release.
	cachedVersion, _ := s.cache.GetCachedVersion()
	metadata, _ := s.cache.GetMetadata()
	if cachedVersion != "" {
		if err := s.reconcileCachedProjection(ctx, cachedVersion, metadata); err != nil {
			return fmt.Errorf("catalog %s is active but its database projection is pending: %w", cachedVersion, err)
		}
	}

	// Check if we have updates available
	remoteVersion, err := s.fetcher.FetchVersion(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch remote version: %w", err)
	}

	// Check if we need to sync. Force permits a same-version repair, but never a
	// downgrade or replay of an older release.
	comparison, versionErr := catalogdistribution.CompareVersions(cachedVersion, remoteVersion)
	if versionErr != nil {
		return versionErr
	}
	if comparison < 0 && force {
		return fmt.Errorf("refusing to apply older catalog version %q over %q", remoteVersion, cachedVersion)
	}
	if comparison <= 0 {
		s.mu.Lock()
		s.currentVersion = cachedVersion
		s.remoteVersion = remoteVersion
		s.hasUpdates = false
		if metadata != nil {
			s.lastSync = metadata.LastSync
			s.newModelsCount = metadata.NewModels
			s.newProvidersCount = metadata.NewProviders
		}
		s.mu.Unlock()
		return nil
	}

	// Fetch full catalog
	files, err := s.fetcher.FetchCatalog(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch catalog: %w", err)
	}
	comparison, versionErr = catalogdistribution.CompareVersions(cachedVersion, files.Version)
	if versionErr != nil {
		return versionErr
	}
	if comparison < 0 {
		return fmt.Errorf("refusing to apply older catalog version %q over %q", files.Version, cachedVersion)
	}
	if comparison == 0 && !force {
		// The channel changed between the version check and bundle fetch. Keep the
		// applied release and wait for the next poll.
		return nil
	}
	remoteVersion = files.Version

	// Merge with the embedded fallback, while comparing changes against the
	// catalog that was actually applied on the previous sync.
	mergeResult, err := s.mergeCatalog(files)
	if err != nil {
		return fmt.Errorf("failed to merge catalogs: %w", err)
	}

	s.mu.RLock()
	catalogRefresher := s.catalogRefresher
	s.mu.RUnlock()

	// Refresh routing against a staged candidate before promoting the durable
	// release. A refresher or cache failure restores the previously active
	// routing catalog and version.
	if catalogRefresher != nil {
		s.mu.Lock()
		s.pendingCatalog = mergeResult
		s.mu.Unlock()
		if err := catalogRefresher.Refresh(); err != nil {
			s.mu.Lock()
			s.pendingCatalog = nil
			s.mu.Unlock()
			return fmt.Errorf("failed to refresh provider catalog: %w", err)
		}
	}

	// Keep the exact release delta inside the atomically promoted bundle. A
	// process stop after activation can then repair projections and reproduce
	// the correct audit event classification without comparing the bundle to
	// itself or relying on separately written metadata.
	files.ProjectionNewModels = append([]string(nil), mergeResult.NewModels...)
	files.ProjectionNewProviders = append([]string(nil), mergeResult.NewProviders...)

	// The atomic bundle rename is the durable activation point. If it fails
	// after the in-memory refresher accepted the candidate, rebuild that catalog
	// from the still-active previous release.
	if err := s.cache.SaveCatalog(files); err != nil {
		s.mu.Lock()
		s.pendingCatalog = nil
		s.mu.Unlock()
		if catalogRefresher != nil {
			if rollbackErr := catalogRefresher.Refresh(); rollbackErr != nil {
				return fmt.Errorf("failed to save catalog: %w; failed to restore provider catalog: %v", err, rollbackErr)
			}
		}
		return fmt.Errorf("failed to save catalog: %w", err)
	}

	// Update metadata without advancing ProjectionVersion. If the process stops
	// before the post-activation database transaction, the next poll can detect
	// and repair the lagging projection.
	metadata, err = s.cache.GetMetadata()
	if err != nil {
		metadata = &CacheMetadata{}
	}
	metadata.Version = files.Version
	metadata.LastCheck = time.Now()
	metadata.LastSync = time.Now()
	metadata.NewModels = len(mergeResult.NewModels)
	metadata.NewProviders = len(mergeResult.NewProviders)
	if err := s.cache.UpdateMetadata(metadata); err != nil {
		logger.Warnf("catalog_sync: failed to update metadata: %v", err)
	}

	// Update service state
	s.mu.Lock()
	s.pendingCatalog = nil
	s.currentVersion = files.Version // Update to the NEW version after sync
	s.remoteVersion = remoteVersion
	// The fetched catalog is already cached and active below.
	// "has updates" therefore cannot remain true after a successful sync.
	s.hasUpdates = false
	s.newModelsCount = len(mergeResult.NewModels)
	s.newProvidersCount = len(mergeResult.NewProviders)
	s.newModelsList = mergeResult.NewModels         // Store list of new models
	s.newProvidersList = mergeResult.NewProviders   // Store list of new providers
	s.updatedModelsList = mergeResult.UpdatedModels // Store list of updated models
	s.deprecatedList = mergeResult.DeprecatedModels // Store list of deprecated items
	s.mergedProviders = mergeResult.Providers       // Store merged providers
	s.lastSync = time.Now()
	s.mu.Unlock()

	if catalogRefresher != nil {
		logger.Infof("catalog_sync: provider catalog refreshed successfully")
	}

	// Projection tables and their existing audit events are intentionally
	// post-activation. They can lag while the database is unavailable, but can
	// never expose a candidate release ahead of the durable routing catalog.
	if err := s.reconcileProjection(ctx, files, mergeResult, metadata); err != nil {
		return fmt.Errorf("catalog %s is active but its database projection is pending: %w", files.Version, err)
	}

	return nil
}

func (s *Service) reconcileCachedProjection(ctx context.Context, version string, metadata *CacheMetadata) error {
	if version == "" {
		return nil
	}
	s.mu.RLock()
	reconcilerConfigured := s.dbReconciler != nil
	s.mu.RUnlock()
	if !reconcilerConfigured {
		return nil
	}
	files, err := s.cache.LoadCatalog()
	if err != nil {
		return fmt.Errorf("load active catalog for projection repair: %w", err)
	}
	if files.Version != version {
		return fmt.Errorf("active catalog version changed from %s to %s during projection repair", version, files.Version)
	}
	mergeResult, err := s.mergeCatalog(files)
	if err != nil {
		return fmt.Errorf("merge active catalog for projection repair: %w", err)
	}
	// The active bundle compares equal to itself, so reconstruct the original
	// release delta from durable metadata. These lists drive both freshness and
	// the existing provider/model audit events on a projection retry.
	mergeResult.NewModels = append([]string(nil), files.ProjectionNewModels...)
	mergeResult.NewProviders = append([]string(nil), files.ProjectionNewProviders...)
	return s.reconcileProjection(ctx, files, mergeResult, metadata)
}

func (s *Service) reconcileProjection(ctx context.Context, files *CatalogFiles, mergeResult *MergeResult, metadata *CacheMetadata) error {
	s.mu.RLock()
	dbReconciler := s.dbReconciler
	s.mu.RUnlock()
	if dbReconciler == nil {
		return nil
	}
	if files == nil {
		return fmt.Errorf("reconcile catalog to DB: active catalog bundle is missing")
	}
	_, bundleSHA256, err := catalogdistribution.BuildBundle(
		files.Version,
		files.Models,
		files.Providers,
		files.Changelog,
	)
	if err != nil {
		return fmt.Errorf("digest active catalog for DB projection: %w", err)
	}
	if err := dbReconciler.ReconcileFromCatalog(ctx, files.Version, bundleSHA256, mergeResult); err != nil {
		return fmt.Errorf("reconcile catalog to DB: %w", err)
	}
	if metadata == nil {
		metadata = &CacheMetadata{Version: files.Version}
	}
	metadata.ProjectionVersion = files.Version
	if err := s.cache.UpdateMetadata(metadata); err != nil {
		// The PostgreSQL journal is authoritative. This local status hint must
		// never block journal recovery or a newer remote release.
		logger.Warnf("catalog_sync: projection completed but local checkpoint update failed: %v", err)
	}
	return nil
}

func (s *Service) mergeCatalog(files *CatalogFiles) (*MergeResult, error) {
	result, err := s.merger.MergeCatalogs(files)
	if err != nil {
		return nil, err
	}
	if !s.cache.HasCache() {
		return result, nil
	}

	cachedFiles, err := s.cache.LoadCatalog()
	if err != nil {
		logger.Warnf("catalog_sync: failed to load applied catalog for change detection: %v", err)
		return result, nil
	}
	cachedModels, err := validator.ParseModelsDefaults(cachedFiles.Models)
	if err != nil {
		logger.Warnf("catalog_sync: failed to parse applied models for change detection: %v", err)
		return result, nil
	}
	var cachedProviders map[string]interface{}
	if len(cachedFiles.Providers) > 0 {
		if err := validator.LoadYAMLIntoStruct(cachedFiles.Providers, &cachedProviders); err != nil {
			logger.Warnf("catalog_sync: failed to parse applied providers for change detection: %v", err)
			return result, nil
		}
	}

	changes, err := NewMerger(cachedModels, cachedProviders).MergeCatalogs(files)
	if err != nil {
		logger.Warnf("catalog_sync: failed to compare catalog with applied version: %v", err)
		return result, nil
	}
	result.NewModels = changes.NewModels
	result.NewProviders = changes.NewProviders
	result.UpdatedModels = changes.UpdatedModels
	result.DeprecatedModels = changes.DeprecatedModels
	return result, nil
}

// SetDBReconciler sets the database reconciler for this service
func (s *Service) SetDBReconciler(reconciler CatalogDBReconciler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dbReconciler = reconciler
}

// SetCatalogRefresher sets the catalog refresher to notify on updates
func (s *Service) SetCatalogRefresher(refresher CatalogRefresher) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.catalogRefresher = refresher
}
