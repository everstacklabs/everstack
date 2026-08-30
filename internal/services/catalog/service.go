package catalog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/catalogdistribution"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Service provides model catalog operations
type Service struct {
	cache         *Cache
	repo          *Repository       // Database repository for persistence
	embeddedFiles map[string][]byte // embedded catalog files
	source        CatalogSource
	sourceMu      sync.RWMutex
}

// NewService creates a new catalog service
func NewService(repo *Repository, embeddedModels, embeddedProviders []byte) *Service {
	return &Service{
		cache: NewCache(),
		repo:  repo,
		embeddedFiles: map[string][]byte{
			"models.yaml":    embeddedModels,
			"providers.yaml": embeddedProviders,
		},
	}
}

// LoadCatalog loads catalog from local sources only. Remote distribution is
// deliberately excluded from the startup path and runs asynchronously after
// the gateway is ready.
//
// Priority: Filesystem (development) -> Database (last known good) -> Embedded.
func (s *Service) LoadCatalog() error {
	ctx := context.Background()

	// 1. Try filesystem (dev mode) - ALWAYS check this first to pick up local changes
	// First try new hierarchical structure, then fall back to legacy flat files
	catalogPath := "model-catalog"
	versionPath := filepath.Join(catalogPath, "version.txt")

	modelsData, providersData, err := s.loadFromFilesystemHierarchical(catalogPath)
	if err != nil {
		// Try legacy flat files
		modelsData, providersData, err = s.loadFromFilesystemLegacy(catalogPath)
	}

	if err == nil {
		// Running from source - use filesystem
		s.setSource(SourceFilesystem)
		logger.Debugf("catalog: loaded from filesystem (local development mode)")

		// Load version if available
		if versionData, err := os.ReadFile(versionPath); err == nil {
			s.cache.SetVersion(strings.TrimSpace(string(versionData)))
		}

		if err := s.cache.Load(modelsData, providersData); err != nil {
			return fmt.Errorf("failed to load catalog from filesystem: %w", err)
		}

		// Sync to database for future use (and to keep DB in sync with local changes)
		if s.repo != nil {
			if err := s.syncToDatabase(ctx, modelsData, providersData, s.cache.GetVersion(), SourceFilesystem); err != nil {
				logger.Warnf("catalog: failed to sync filesystem catalog to database: %v", err)
			} else {
				logger.Debugf("catalog: synced filesystem changes to database")
			}
		}

		return nil
	}

	// 2. Try loading from database (fastest - cached catalog for production)
	if s.repo != nil {
		if err := s.loadFromDatabase(ctx); err == nil {
			s.setSource(SourceDatabase)
			logger.Debugf("catalog: loaded from database (cached)")
			return nil
		}
		logger.Debugf("catalog: database load failed or empty, trying other sources")
	}

	// 3. Fall back to the catalog embedded in the gateway binary. Startup must
	// never depend on the availability of the remote distribution channel.
	if s.embeddedFiles["models.yaml"] != nil && s.embeddedFiles["providers.yaml"] != nil {
		s.setSource(SourceEmbedded)
		logger.Debugf("catalog: loaded from embedded binary (production mode)")

		if err := s.cache.Load(s.embeddedFiles["models.yaml"], s.embeddedFiles["providers.yaml"]); err != nil {
			return fmt.Errorf("failed to load embedded catalog: %w", err)
		}

		// Sync to database for future use
		if s.repo != nil {
			if err := s.syncToDatabase(ctx, s.embeddedFiles["models.yaml"], s.embeddedFiles["providers.yaml"], s.cache.GetVersion(), SourceEmbedded); err != nil {
				logger.Warnf("catalog: failed to sync embedded catalog to database: %v", err)
			}
		}

		return nil
	}

	// 4. Last resort: minimal hardcoded fallback
	logger.Warnf("catalog: all sources failed, using minimal fallback")
	return s.loadFallback()
}

// GetModels returns cached model definitions
func (s *Service) GetModels() *Cache {
	return s.cache
}

// GetProviders returns cached provider definitions
func (s *Service) GetProviders() *Cache {
	return s.cache
}

// ValidateModel checks if a model exists in the catalog
func (s *Service) ValidateModel(provider, model string) error {
	if _, ok := s.cache.GetModel(provider, model); !ok {
		// Get available models for helpful error
		if models, ok := s.cache.GetAllModels(provider); ok && len(models) > 0 {
			modelNames := make([]string, 0, len(models))
			for _, m := range models {
				modelNames = append(modelNames, m.Name)
			}
			return fmt.Errorf("model '%s' not found in catalog for provider '%s'. Available models: %v",
				model, provider, modelNames)
		}
		return fmt.Errorf("model '%s' not found in catalog for provider '%s' (provider has no models)",
			model, provider)
	}
	return nil
}

// ValidateProvider checks if a provider is supported
func (s *Service) ValidateProvider(provider string) error {
	if _, ok := s.cache.GetProvider(provider); !ok {
		// Get available providers for helpful error
		allProviders := s.cache.GetAllProviders()
		providerNames := make([]string, 0, len(allProviders))
		for _, p := range allProviders {
			providerNames = append(providerNames, p.Name)
		}
		return fmt.Errorf("provider '%s' not supported. Available providers: %v",
			provider, providerNames)
	}
	return nil
}

// GetSource returns the catalog source ("filesystem" or "embedded")
func (s *Service) GetSource() string {
	s.sourceMu.RLock()
	defer s.sourceMu.RUnlock()
	return string(s.source)
}

func (s *Service) setSource(source CatalogSource) {
	s.sourceMu.Lock()
	defer s.sourceMu.Unlock()
	s.source = source
}

// GetVersion returns the catalog version
func (s *Service) GetVersion() string {
	return s.cache.GetVersion()
}

// GetCache returns the underlying cache (for direct access)
func (s *Service) GetCache() *Cache {
	return s.cache
}

// StartBackgroundSync starts a background job to sync catalog from remote
// This will be implemented in sync.go
func (s *Service) StartBackgroundSync(ctx context.Context, interval string, syncService *RemoteSyncService) {
	if syncService == nil {
		logger.Warn("catalog: background sync disabled (sync service not initialized)")
		return
	}

	go syncService.StartPeriodicSync(ctx, s, interval)
}

func (s *Service) applyRemoteCatalog(ctx context.Context, models, providers []byte, version string) error {
	if err := catalogdistribution.ValidateCatalogDocuments(models, providers); err != nil {
		return fmt.Errorf("failed to validate remote catalog completeness: %w", err)
	}
	validated := NewCache()
	if err := validated.Load(models, providers); err != nil {
		return fmt.Errorf("failed to validate remote catalog: %w", err)
	}
	validated.SetVersion(version)
	if err := s.syncToDatabase(ctx, models, providers, version, SourceRemote); err != nil {
		return fmt.Errorf("failed to persist last-known-good remote catalog: %w", err)
	}
	if err := s.cache.Refresh(models, providers); err != nil {
		return fmt.Errorf("failed to activate remote catalog: %w", err)
	}
	s.cache.SetVersion(version)
	s.setSource(SourceRemote)
	return nil
}

// ReloadFromFilesystem forces a reload from filesystem (for testing/dev)
func (s *Service) ReloadFromFilesystem() error {
	catalogPath := "model-catalog"
	versionPath := filepath.Join(catalogPath, "version.txt")

	// Try new hierarchical structure first, then fall back to legacy
	modelsData, providersData, err := s.loadFromFilesystemHierarchical(catalogPath)
	if err != nil {
		modelsData, providersData, err = s.loadFromFilesystemLegacy(catalogPath)
		if err != nil {
			return fmt.Errorf("failed to load catalog from filesystem: %w", err)
		}
	}

	// Load version
	if versionData, err := os.ReadFile(versionPath); err == nil {
		s.cache.SetVersion(strings.TrimSpace(string(versionData)))
	}

	return s.cache.Refresh(modelsData, providersData)
}

// GetModelWhitelist returns a whitelist map for bootstrap validation
func (s *Service) GetModelWhitelist() map[string]map[string]struct{} {
	return s.cache.GetModelWhitelist()
}

// LoadCatalogFromPath loads catalog from a specific directory path
func (s *Service) LoadCatalogFromPath(catalogDir string) error {
	versionPath := filepath.Join(catalogDir, "version.txt")

	// Try new hierarchical structure first, then fall back to legacy
	modelsData, providersData, err := s.loadFromFilesystemHierarchical(catalogDir)
	if err != nil {
		modelsData, providersData, err = s.loadFromFilesystemLegacy(catalogDir)
		if err != nil {
			return fmt.Errorf("failed to load catalog from %s: %w", catalogDir, err)
		}
	}

	// Load version
	if versionData, err := os.ReadFile(versionPath); err == nil {
		s.cache.SetVersion(strings.TrimSpace(string(versionData)))
	}

	s.setSource(SourceFilesystem)
	logger.Debugf("catalog: loaded from directory: %s", catalogDir)

	return s.cache.Load(modelsData, providersData)
}

// loadFromFilesystemHierarchical loads catalog from the new providers/ directory structure
func (s *Service) loadFromFilesystemHierarchical(catalogPath string) ([]byte, []byte, error) {
	providersDir := filepath.Join(catalogPath, "providers")
	if _, err := os.Stat(providersDir); os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("providers directory not found")
	}

	// Use the validator's catalog loader which handles the hierarchical structure
	return validator.LoadCatalogFromDirectory(catalogPath)
}

// loadFromFilesystemLegacy loads catalog from legacy flat files (models.yaml, providers.yaml)
func (s *Service) loadFromFilesystemLegacy(catalogPath string) ([]byte, []byte, error) {
	modelsPath := filepath.Join(catalogPath, "models.yaml")
	providersPath := filepath.Join(catalogPath, "providers.yaml")

	modelsData, err := os.ReadFile(modelsPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read models.yaml: %w", err)
	}

	providersData, err := os.ReadFile(providersPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read providers.yaml: %w", err)
	}

	return modelsData, providersData, nil
}
