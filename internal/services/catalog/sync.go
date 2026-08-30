package catalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/catalogdistribution"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/modelidentity"
	"gopkg.in/yaml.v3"
)

const (
	// DefaultRepoURL is the independently hosted signed catalog data plane.
	DefaultRepoURL = "https://catalog.everstack.ai/v1"
)

// CatalogManifest represents the manifest.yaml structure for remote sync
type CatalogManifest struct {
	Version       string             `yaml:"version"`
	GeneratedAt   string             `yaml:"generated_at"`
	SchemaVersion string             `yaml:"schema_version"`
	Providers     []ManifestProvider `yaml:"providers"`
}

// ManifestProvider represents a provider entry in the manifest
type ManifestProvider struct {
	Name   string   `yaml:"name"`
	Files  []string `yaml:"files"`
	Models []string `yaml:"models"`
}

type remoteHTTPError struct {
	statusCode int
	status     string
}

func (e *remoteHTTPError) Error() string {
	return fmt.Sprintf("http %d: %s", e.statusCode, e.status)
}

// RemoteSyncService handles asynchronous catalog distribution updates.
type RemoteSyncService struct {
	repoURL       string
	pinnedVersion string
	client        *http.Client

	distribution    *catalogdistribution.Client
	distributionErr error
}

// NewRemoteSyncService creates a new remote sync service
// repoURL: custom distribution URL (empty for default)
func NewRemoteSyncService(repoURL string) *RemoteSyncService {
	return newRemoteSyncService(repoURL, nil)
}

// NewRemoteSyncServiceWithTrust uses the fully merged gateway trust policy
// instead of reading process environment directly.
func NewRemoteSyncServiceWithTrust(repoURL string, trust catalogdistribution.TrustConfig) *RemoteSyncService {
	return newRemoteSyncService(repoURL, &trust)
}

func newRemoteSyncService(repoURL string, trust *catalogdistribution.TrustConfig) *RemoteSyncService {
	if repoURL == "" {
		repoURL = DefaultRepoURL
	}

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}
	var distribution *catalogdistribution.Client
	var distributionErr error
	if trust == nil {
		distribution, distributionErr = catalogdistribution.NewClientFromEnvironment(repoURL, httpClient)
	} else {
		distribution, distributionErr = catalogdistribution.NewClientFromTrustConfig(repoURL, httpClient, *trust)
	}

	return &RemoteSyncService{
		repoURL: repoURL,
		client:  httpClient,

		distribution:    distribution,
		distributionErr: distributionErr,
	}
}

// NewRemoteSyncServiceWithVersion is retained for source compatibility.
// Deprecated: immutable version pinning through GitHub is no longer a runtime
// path; publish the desired content as a higher signed channel release.
func NewRemoteSyncServiceWithVersion(version string) *RemoteSyncService {
	service := newRemoteSyncService(DefaultRepoURL, nil)
	service.pinnedVersion = version
	service.distribution = nil
	service.distributionErr = fmt.Errorf("pinned catalog version %q is unsupported; promote a signed channel release", version)
	return service
}

// GetPinnedVersion returns the deprecated configured pin, if any.
func (s *RemoteSyncService) GetPinnedVersion() string {
	return s.pinnedVersion
}

// IsPinned reports whether the deprecated version-pin constructor was used.
func (s *RemoteSyncService) IsPinned() bool {
	return s.pinnedVersion != ""
}

// CheckForUpdates compares local vs remote version
func (s *RemoteSyncService) CheckForUpdates(currentVersion string) (bool, string, error) {
	return s.CheckForUpdatesContext(context.Background(), currentVersion)
}

func (s *RemoteSyncService) CheckForUpdatesContext(ctx context.Context, currentVersion string) (bool, string, error) {
	if s.distributionErr != nil {
		return false, "", fmt.Errorf("invalid catalog distribution configuration: %w", s.distributionErr)
	}
	if s.distribution != nil {
		remoteVersion, err := s.distribution.FetchVersion(ctx)
		if err != nil {
			return false, "", err
		}
		hasUpdate, err := catalogdistribution.IsNewerVersion(currentVersion, remoteVersion)
		return hasUpdate, remoteVersion, err
	}

	// Fetch remote version
	remoteVersion, err := s.fetchFile("version.txt")
	if err != nil {
		return false, "", fmt.Errorf("failed to fetch remote version: %w", err)
	}

	remoteVersionStr := strings.TrimSpace(string(remoteVersion))

	hasUpdate, err := catalogdistribution.IsNewerVersion(currentVersion, remoteVersionStr)
	return hasUpdate, remoteVersionStr, err
}

// SyncFromRemote fetches catalog from remote and returns the data
// It first tries manifest-based sync, then falls back to legacy flat files
func (s *RemoteSyncService) SyncFromRemote() (models, providers []byte, version string, err error) {
	return s.SyncFromRemoteContext(context.Background())
}

func (s *RemoteSyncService) SyncFromRemoteContext(ctx context.Context) (models, providers []byte, version string, err error) {
	if s.distributionErr != nil {
		return nil, nil, "", fmt.Errorf("invalid catalog distribution configuration: %w", s.distributionErr)
	}
	if s.distribution != nil {
		bundle, err := s.distribution.Fetch(ctx)
		if err != nil {
			return nil, nil, "", err
		}
		return bundle.Models, bundle.Providers, bundle.Version, nil
	}

	// Try manifest-based sync first (new hierarchical structure)
	modelsData, providersData, version, err := s.syncFromManifest()
	if err == nil {
		if err := catalogdistribution.ValidateCatalogDocuments(modelsData, providersData); err != nil {
			return nil, nil, "", fmt.Errorf("validate hierarchical catalog: %w", err)
		}
		return modelsData, providersData, version, nil
	}

	var httpError *remoteHTTPError
	if errors.As(err, &httpError) && httpError.statusCode == http.StatusNotFound {
		logger.Infof("catalog_sync: manifest not found, using legacy flat catalog")
		modelsData, providersData, version, legacyErr := s.syncFromLegacyFiles()
		if legacyErr != nil {
			return nil, nil, "", legacyErr
		}
		if validationErr := catalogdistribution.ValidateCatalogDocuments(modelsData, providersData); validationErr != nil {
			return nil, nil, "", fmt.Errorf("validate legacy catalog: %w", validationErr)
		}
		return modelsData, providersData, version, nil
	}
	return nil, nil, "", err
}

// syncFromManifest fetches catalog using the manifest.yaml approach
func (s *RemoteSyncService) syncFromManifest() (models, providers []byte, version string, err error) {
	// Fetch manifest.yaml
	manifestData, err := s.fetchFile("manifest.yaml")
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to fetch manifest.yaml: %w", err)
	}

	var manifest CatalogManifest
	if err := yaml.Unmarshal(manifestData, &manifest); err != nil {
		return nil, nil, "", fmt.Errorf("failed to parse manifest.yaml: %w", err)
	}

	version = strings.TrimSpace(manifest.Version)
	versionData, err := s.fetchFile("version.txt")
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to fetch version.txt for manifest release: %w", err)
	}
	if publishedVersion := strings.TrimSpace(string(versionData)); publishedVersion != version {
		return nil, nil, "", fmt.Errorf("catalog manifest version is %q, version.txt is %q", version, publishedVersion)
	}

	// Fetch all provider and model files in parallel
	type fileResult struct {
		path string
		data []byte
		err  error
	}

	var allFiles []string
	for _, provider := range manifest.Providers {
		allFiles = append(allFiles, provider.Files...)
		allFiles = append(allFiles, provider.Models...)
	}

	results := make(chan fileResult, len(allFiles))
	var wg sync.WaitGroup

	// Limit concurrent requests
	semaphore := make(chan struct{}, 10)

	for _, filePath := range allFiles {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			data, fetchErr := s.fetchFile(path)
			results <- fileResult{path: path, data: data, err: fetchErr}
		}(filePath)
	}

	// Close results channel when all goroutines complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	fileContents := make(map[string][]byte)
	failedFiles := make([]string, 0)
	for result := range results {
		if result.err != nil {
			logger.Warnf("catalog_sync: failed to fetch %s: %v", result.path, result.err)
			failedFiles = append(failedFiles, result.path)
			continue
		}
		fileContents[result.path] = result.data
	}
	if len(failedFiles) > 0 {
		sort.Strings(failedFiles)
		return nil, nil, "", fmt.Errorf(
			"catalog release %s is incomplete; failed to fetch %d declared files: %s",
			version,
			len(failedFiles),
			strings.Join(failedFiles, ", "),
		)
	}

	// Aggregate into legacy format for backward compatibility
	modelsData, providersData, err := s.aggregateCatalogFiles(manifest.Providers, fileContents)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to aggregate catalog files: %w", err)
	}

	return modelsData, providersData, version, nil
}

// syncFromLegacyFiles fetches catalog using legacy flat files
func (s *RemoteSyncService) syncFromLegacyFiles() (models, providers []byte, version string, err error) {
	// Fetch models.yaml
	modelsData, err := s.fetchFile("models.yaml")
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to fetch models.yaml: %w", err)
	}

	// Fetch providers.yaml
	providersData, err := s.fetchFile("providers.yaml")
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to fetch providers.yaml: %w", err)
	}

	// Fetch version.txt
	versionData, err := s.fetchFile("version.txt")
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to fetch version.txt: %w", err)
	}
	version = strings.TrimSpace(string(versionData))

	return modelsData, providersData, version, nil
}

// aggregateCatalogFiles combines fetched files into legacy models/providers format
func (s *RemoteSyncService) aggregateCatalogFiles(providers []ManifestProvider, files map[string][]byte) (models, providersYAML []byte, err error) {
	modelsConfig := map[string]interface{}{
		"providers": make(map[string]interface{}),
	}
	providersConfig := map[string]interface{}{
		"providers": make(map[string]interface{}),
	}

	for _, provider := range providers {
		// Find provider.yaml file
		var providerData map[string]interface{}
		for _, filePath := range provider.Files {
			if strings.HasSuffix(filePath, "provider.yaml") {
				data, ok := files[filePath]
				if !ok {
					return nil, nil, fmt.Errorf("catalog release is missing declared file %s", filePath)
				}
				if err := yaml.Unmarshal(data, &providerData); err != nil {
					return nil, nil, fmt.Errorf("failed to parse %s: %w", filePath, err)
				}
				break
			}
		}

		if providerData == nil {
			return nil, nil, fmt.Errorf("catalog release has no provider.yaml for %s", provider.Name)
		}

		// Load all models for this provider
		var modelsList []map[string]interface{}
		for _, modelPath := range provider.Models {
			data, ok := files[modelPath]
			if !ok {
				return nil, nil, fmt.Errorf("catalog release is missing declared file %s", modelPath)
			}
			var modelData map[string]interface{}
			if err := yaml.Unmarshal(data, &modelData); err != nil {
				return nil, nil, fmt.Errorf("failed to parse %s: %w", modelPath, err)
			}
			// Convert to legacy format
			legacyModel := convertModelToLegacy(provider.Name, modelData)
			modelsList = append(modelsList, legacyModel)
		}

		// Build legacy models provider entry
		modelsConfig["providers"].(map[string]interface{})[provider.Name] = map[string]interface{}{
			"name":     providerData["display_name"],
			"base_url": providerData["base_url"],
			"models":   modelsList,
		}

		// Build legacy providers entry
		providersConfig["providers"].(map[string]interface{})[provider.Name] = providerData
	}

	models, err = yaml.Marshal(modelsConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal models: %w", err)
	}

	providersYAML, err = yaml.Marshal(providersConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal providers: %w", err)
	}

	return models, providersYAML, nil
}

// convertModelToLegacy converts new model format to legacy format
func convertModelToLegacy(provider string, model map[string]interface{}) map[string]interface{} {
	modelName, _ := model["name"].(string)
	publisher, _ := model["publisher"].(string)
	canonicalModelID, _ := model["canonical_model_id"].(string)
	identity := modelidentity.ResolveWithOverrides(
		provider,
		modelName,
		publisher,
		canonicalModelID,
	)
	legacy := map[string]interface{}{
		"name":               model["name"],
		"display_name":       model["display_name"],
		"status":             model["status"],
		"publisher":          identity.Publisher,
		"canonical_model_id": identity.CanonicalModelID,
	}
	if releaseDate, ok := model["release_date"]; ok {
		legacy["release_date"] = releaseDate
	}

	// Handle cost
	if cost, ok := model["cost"].(map[string]interface{}); ok {
		if v, ok := cost["input_per_1k"]; ok {
			legacy["input_cost_per_1k"] = v
		}
		if v, ok := cost["output_per_1k"]; ok {
			legacy["output_cost_per_1k"] = v
		}
		// Optional: absent for providers that do not price cache reads, and
		// for catalogs synced before these fields existed.
		if v, ok := cost["cache_read_per_1k"]; ok {
			legacy["cache_read_cost_per_1k"] = v
		}
		if v, ok := cost["cache_write_per_1k"]; ok {
			legacy["cache_write_cost_per_1k"] = v
		}
	}

	// Handle limits
	if limits, ok := model["limits"].(map[string]interface{}); ok {
		if v, ok := limits["max_tokens"]; ok {
			legacy["max_tokens"] = v
		}
	}

	// Handle capabilities
	if caps, ok := model["capabilities"]; ok {
		legacy["capabilities"] = caps
	}

	return legacy
}

// DownloadAndCache fetches from remote and updates the cache
func (s *RemoteSyncService) DownloadAndCache(cache *Cache) error {
	logger.Info("catalog_sync: downloading catalog from remote distribution")

	models, providers, version, err := s.SyncFromRemote()
	if err != nil {
		return fmt.Errorf("failed to sync from remote: %w", err)
	}
	hasUpdate, err := catalogdistribution.IsNewerVersion(cache.GetVersion(), version)
	if err != nil {
		return err
	}
	if !hasUpdate {
		return fmt.Errorf("refusing to cache non-newer catalog version %q", version)
	}

	// Update cache
	if err := cache.Refresh(models, providers); err != nil {
		return fmt.Errorf("failed to refresh cache: %w", err)
	}

	// Update version
	if version != "" {
		cache.SetVersion(version)
	}

	logger.Infof("catalog_sync: successfully synced catalog version %s", version)
	return nil
}

func (s *RemoteSyncService) downloadAndApply(ctx context.Context, service *Service) error {
	logger.Info("catalog_sync: downloading catalog from remote distribution")

	models, providers, version, err := s.SyncFromRemoteContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to sync from remote: %w", err)
	}
	hasUpdate, err := catalogdistribution.IsNewerVersion(service.GetVersion(), version)
	if err != nil {
		return err
	}
	if !hasUpdate {
		return fmt.Errorf("refusing to apply non-newer catalog version %q", version)
	}
	if err := service.applyRemoteCatalog(ctx, models, providers, version); err != nil {
		return err
	}

	logger.Infof("catalog_sync: successfully applied catalog version %s", version)
	return nil
}

// fetchFile fetches a single file from the remote repository
func (s *RemoteSyncService) fetchFile(filename string) ([]byte, error) {
	url := fmt.Sprintf("%s/%s", s.repoURL, filename)

	resp, err := s.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &remoteHTTPError{statusCode: resp.StatusCode, status: resp.Status}
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return data, nil
}

// StartPeriodicSync runs a background sync job at the specified interval
func (s *RemoteSyncService) StartPeriodicSync(ctx context.Context, service *Service, intervalStr string) {
	interval, err := time.ParseDuration(intervalStr)
	if err != nil || interval <= 0 {
		logger.Errorf("catalog_sync: invalid interval %q, defaulting to 5m", intervalStr)
		interval = 5 * time.Minute
	}

	logger.Infof("catalog_sync: starting periodic sync job (interval: %s)", interval)

	// Give startup traffic a short quiet window without making shutdown wait for
	// the first refresh timer.
	initialDelay := 30 * time.Second
	if interval < initialDelay {
		initialDelay = interval
	}
	initialSync := time.NewTimer(initialDelay)
	defer initialSync.Stop()
	select {
	case <-ctx.Done():
		logger.Info("catalog_sync: stopping periodic sync job")
		return
	case <-initialSync.C:
		s.syncOnce(ctx, service)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("catalog_sync: stopping periodic sync job")
			return
		case <-ticker.C:
			s.syncOnce(ctx, service)
		}
	}
}

// syncOnce performs a single sync operation
func (s *RemoteSyncService) syncOnce(ctx context.Context, service *Service) {
	logger.Info("catalog_sync: starting scheduled sync")

	currentVersion := service.GetVersion()

	// Check for updates
	hasUpdate, remoteVersion, err := s.CheckForUpdatesContext(ctx, currentVersion)
	if err != nil {
		logger.Errorf("catalog_sync: failed to check for updates: %v", err)
		return
	}

	if !hasUpdate {
		logger.Infof("catalog_sync: catalog is up-to-date (version: %s)", currentVersion)
		return
	}

	logger.Infof("catalog_sync: new version available: %s (current: %s)", remoteVersion, currentVersion)

	// Download and cache
	if err := s.downloadAndApply(ctx, service); err != nil {
		logger.Errorf("catalog_sync: failed to download and cache: %v", err)
		return
	}

	logger.Infof("catalog_sync: successfully updated catalog to version %s", remoteVersion)
}

// TriggerManualSync triggers an immediate sync (for UI "Sync Now" button)
func (s *RemoteSyncService) TriggerManualSync(cache *Cache) error {
	logger.Info("catalog_sync: manual sync triggered")

	currentVersion := cache.GetVersion()

	// Check for updates
	hasUpdate, remoteVersion, err := s.CheckForUpdates(currentVersion)
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	if !hasUpdate {
		logger.Infof("catalog_sync: catalog is already up-to-date (version: %s)", currentVersion)
		return nil
	}

	logger.Infof("catalog_sync: new version available: %s (current: %s)", remoteVersion, currentVersion)

	// Download and cache
	if err := s.DownloadAndCache(cache); err != nil {
		return fmt.Errorf("failed to download and cache: %w", err)
	}

	logger.Infof("catalog_sync: successfully updated catalog to version %s", remoteVersion)
	return nil
}
