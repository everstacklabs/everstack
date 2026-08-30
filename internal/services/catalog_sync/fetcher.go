package catalog_sync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/catalogdistribution"
	"github.com/everstacklabs/everstack/internal/modelidentity"
	"gopkg.in/yaml.v3"
)

// Fetcher handles fetching catalog files from remote URL or local filesystem
type Fetcher struct {
	client     *http.Client
	baseURL    string
	basePath   string
	isLocal    bool
	maxRetries int
	retryDelay time.Duration

	distribution    *catalogdistribution.Client
	distributionErr error
}

// CatalogManifest represents the manifest.yaml structure
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

type fetchHTTPError struct {
	statusCode int
	filename   string
}

func (e *fetchHTTPError) Error() string {
	return fmt.Sprintf("failed to fetch %s: status %d", e.filename, e.statusCode)
}

// NewFetcher creates a new remote catalog fetcher
func NewFetcher(baseURL string, timeout time.Duration, maxRetries int, retryDelay time.Duration, trustConfigs ...catalogdistribution.TrustConfig) *Fetcher {
	httpClient := &http.Client{Timeout: timeout}
	var distribution *catalogdistribution.Client
	var distributionErr error
	if len(trustConfigs) > 0 {
		distribution, distributionErr = catalogdistribution.NewClientFromTrustConfig(baseURL, httpClient, trustConfigs[0])
	} else {
		distribution, distributionErr = catalogdistribution.NewClientFromEnvironment(baseURL, httpClient)
	}

	return &Fetcher{
		client:     httpClient,
		baseURL:    baseURL,
		isLocal:    false,
		maxRetries: maxRetries,
		retryDelay: retryDelay,

		distribution:    distribution,
		distributionErr: distributionErr,
	}
}

// NewLocalFetcher creates a new local filesystem catalog fetcher
func NewLocalFetcher(basePath string) *Fetcher {
	return &Fetcher{
		basePath: basePath,
		isLocal:  true,
	}
}

// CatalogFiles represents the catalog files
type CatalogFiles struct {
	Models                 []byte   `json:"models"`
	Providers              []byte   `json:"providers"`
	Version                string   `json:"version"`
	Changelog              []byte   `json:"changelog"`
	ProjectionNewModels    []string `json:"projection_new_models,omitempty"`
	ProjectionNewProviders []string `json:"projection_new_providers,omitempty"`
}

// FetchCatalog fetches all catalog files from remote URL using manifest-based approach
func (f *Fetcher) FetchCatalog(ctx context.Context) (*CatalogFiles, error) {
	if f.distributionErr != nil {
		return nil, fmt.Errorf("invalid catalog distribution configuration: %w", f.distributionErr)
	}
	if f.distribution != nil {
		bundle, err := f.distribution.Fetch(ctx)
		if err != nil {
			return nil, err
		}
		return &CatalogFiles{
			Models:    bundle.Models,
			Providers: bundle.Providers,
			Version:   bundle.Version,
			Changelog: bundle.Changelog,
		}, nil
	}

	files := &CatalogFiles{}

	// Fetch version first
	version, err := f.fetchFile(ctx, "version.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch version: %w", err)
	}
	files.Version = strings.TrimSpace(string(version))

	// Fetch changelog before aggregating hierarchical files so models added in
	// this release retain their durable "new" marker after a remote sync.
	changelog, changelogErr := f.fetchFile(ctx, "changelog.yaml")
	if changelogErr == nil {
		files.Changelog = changelog
	}
	latestAdditions, err := catalogAdditionsForVersion(
		files.Changelog,
		files.Version,
	)
	if err != nil {
		return nil, err
	}

	// Try manifest-based fetch first (new hierarchical structure)
	models, providers, err := f.fetchFromManifest(ctx, files.Version, latestAdditions)
	if err == nil {
		files.Models = models
		files.Providers = providers
	} else if f.manifestUnavailable(err) {
		// Fallback to legacy flat files
		models, err := f.fetchFile(ctx, "models.yaml")
		if err != nil {
			return nil, fmt.Errorf("failed to fetch models: %w", err)
		}
		files.Models = models

		providers, err := f.fetchFile(ctx, "providers.yaml")
		if err != nil {
			return nil, fmt.Errorf("failed to fetch providers: %w", err)
		}
		files.Providers = providers
	} else {
		return nil, fmt.Errorf("failed to fetch hierarchical catalog: %w", err)
	}

	if err := catalogdistribution.ValidateCatalogDocuments(files.Models, files.Providers); err != nil {
		return nil, fmt.Errorf("validate fetched catalog: %w", err)
	}
	return files, nil
}

// fetchFromManifest fetches catalog using the manifest.yaml approach
func (f *Fetcher) fetchFromManifest(
	ctx context.Context,
	expectedVersion string,
	latestAdditions map[string]string,
) (models, providers []byte, err error) {
	// Fetch manifest.yaml
	manifestData, err := f.fetchFile(ctx, "manifest.yaml")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch manifest.yaml: %w", err)
	}

	var manifest CatalogManifest
	if err := yaml.Unmarshal(manifestData, &manifest); err != nil {
		return nil, nil, fmt.Errorf("failed to parse manifest.yaml: %w", err)
	}
	if manifest.Version != expectedVersion {
		return nil, nil, fmt.Errorf("catalog manifest version is %q, version.txt is %q", manifest.Version, expectedVersion)
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

			data, fetchErr := f.fetchFile(ctx, path)
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
			failedFiles = append(failedFiles, result.path)
			continue
		}
		fileContents[result.path] = result.data
	}
	if len(failedFiles) > 0 {
		sort.Strings(failedFiles)
		return nil, nil, fmt.Errorf(
			"catalog release is incomplete; failed to fetch %d declared files: %s",
			len(failedFiles),
			strings.Join(failedFiles, ", "),
		)
	}

	// Aggregate into legacy format for backward compatibility
	return f.aggregateCatalogFiles(manifest.Providers, fileContents, latestAdditions)
}

// aggregateCatalogFiles combines fetched files into legacy models/providers format
func (f *Fetcher) aggregateCatalogFiles(
	providers []ManifestProvider,
	files map[string][]byte,
	latestAdditions map[string]string,
) (models, providersYAML []byte, err error) {
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
			modelName, _ := modelData["name"].(string)
			legacyModel := convertModelToLegacy(
				provider.Name,
				modelData,
				latestAdditions[provider.Name+"/"+modelName],
			)
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
func convertModelToLegacy(
	provider string,
	model map[string]interface{},
	addedInVersion string,
) map[string]interface{} {
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
		"publisher":          identity.Publisher,
		"canonical_model_id": identity.CanonicalModelID,
		"status":             model["status"],
	}
	if releaseDate, ok := model["release_date"]; ok {
		legacy["release_date"] = releaseDate
	}
	if addedInVersion != "" {
		legacy["added_in_version"] = addedInVersion
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

func catalogAdditionsForVersion(
	data []byte,
	version string,
) (map[string]string, error) {
	additions := make(map[string]string)
	changelog, err := ParseChangelog(data)
	if err != nil {
		return nil, err
	}
	for _, entry := range changelog.Versions {
		if entry.Version != version {
			continue
		}
		for _, model := range entry.Changes.NewModels {
			additions[model.Provider+"/"+model.Model] = version
		}
		break
	}
	return additions, nil
}

// FetchVersion fetches only the version file
func (f *Fetcher) FetchVersion(ctx context.Context) (string, error) {
	if f.distributionErr != nil {
		return "", fmt.Errorf("invalid catalog distribution configuration: %w", f.distributionErr)
	}
	if f.distribution != nil {
		return f.distribution.FetchVersion(ctx)
	}

	version, err := f.fetchFile(ctx, "version.txt")
	if err != nil {
		return "", fmt.Errorf("failed to fetch version: %w", err)
	}
	return strings.TrimSpace(string(version)), nil
}

// fetchFile fetches a single file with retry logic
func (f *Fetcher) fetchFile(ctx context.Context, filename string) ([]byte, error) {
	// For local filesystem, read directly
	if f.isLocal {
		return f.fetchLocalFile(filename)
	}

	// For remote, use HTTP with retry logic
	url := fmt.Sprintf("%s/%s", f.baseURL, filename)

	var lastErr error
	for attempt := 0; attempt <= f.maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(f.retryDelay * time.Duration(attempt)):
			}
		}

		data, err := f.doFetch(ctx, url, filename)
		if err == nil {
			return data, nil
		}
		var httpError *fetchHTTPError
		if errors.As(err, &httpError) && httpError.statusCode == http.StatusNotFound {
			return nil, err
		}
		lastErr = err
	}

	return nil, fmt.Errorf("failed after %d attempts: %w", f.maxRetries+1, lastErr)
}

// fetchLocalFile reads a file from the local filesystem
func (f *Fetcher) fetchLocalFile(filename string) ([]byte, error) {
	path := filepath.Join(f.basePath, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read local file %s: %w", path, err)
	}
	// Trim trailing newlines/whitespace from version string
	if filename == "version.txt" {
		data = []byte(strings.TrimSpace(string(data)))
	}
	return data, nil
}

// doFetch performs a single fetch attempt
func (f *Fetcher) doFetch(ctx context.Context, url, filename string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add headers to prevent caching issues
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("User-Agent", "Everstack-Gateway/1.0")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %s: %w", filename, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &fetchHTTPError{statusCode: resp.StatusCode, filename: filename}
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response for %s: %w", filename, err)
	}

	return data, nil
}

func (f *Fetcher) manifestUnavailable(err error) bool {
	if f.isLocal && errors.Is(err, os.ErrNotExist) {
		return true
	}
	var httpError *fetchHTTPError
	return errors.As(err, &httpError) && httpError.statusCode == http.StatusNotFound
}
