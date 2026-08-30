package catalog_sync

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/catalogdistribution"
)

const catalogBundleFilename = "catalog.bundle.json"

// Cache manages local filesystem cache for catalog files
type Cache struct {
	cacheDir string
	mu       sync.RWMutex
}

// NewCache creates a new cache manager
func NewCache(cacheDir string) *Cache {
	return &Cache{
		cacheDir: cacheDir,
	}
}

// CacheMetadata holds metadata about cached files
type CacheMetadata struct {
	Version           string    `json:"version"`
	ProjectionVersion string    `json:"projection_version,omitempty"`
	LastCheck         time.Time `json:"last_check"`
	LastSync          time.Time `json:"last_sync"`
	NewModels         int       `json:"new_models"`
	NewProviders      int       `json:"new_providers"`
}

// EnsureCacheDir ensures the cache directory exists
func (c *Cache) EnsureCacheDir() error {
	return os.MkdirAll(c.cacheDir, 0755)
}

// SaveCatalog atomically promotes one complete catalog bundle. Legacy cache
// files are read only as a migration path and are never rewritten, because a
// crash during a multi-file update could leave a mixed release behind.
func (c *Cache) SaveCatalog(files *CatalogFiles) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if files == nil || files.Version == "" || len(files.Models) == 0 || len(files.Providers) == 0 {
		return fmt.Errorf("refusing to cache incomplete catalog bundle")
	}
	if err := catalogdistribution.ValidateCatalogDocuments(files.Models, files.Providers); err != nil {
		return fmt.Errorf("refusing to cache invalid catalog bundle: %w", err)
	}
	if err := c.EnsureCacheDir(); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}
	bundle, err := json.Marshal(files)
	if err != nil {
		return fmt.Errorf("failed to encode atomic catalog bundle: %w", err)
	}

	// This rename is the only catalog release commit point. Status
	// metadata is ancillary and is updated by the sync service after this
	// succeeds.
	if err := c.saveFile(catalogBundleFilename, bundle); err != nil {
		return fmt.Errorf("failed to save atomic catalog bundle: %w", err)
	}

	return nil
}

// LoadCatalog loads catalog files from cache
func (c *Cache) LoadCatalog() (*CatalogFiles, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	bundle, err := c.loadFile(catalogBundleFilename)
	if err == nil {
		files := &CatalogFiles{}
		if err := json.Unmarshal(bundle, files); err != nil {
			return nil, fmt.Errorf("failed to decode atomic catalog bundle: %w", err)
		}
		if files.Version == "" || len(files.Models) == 0 || len(files.Providers) == 0 {
			return nil, fmt.Errorf("atomic catalog bundle is incomplete")
		}
		if err := catalogdistribution.ValidateCatalogDocuments(files.Models, files.Providers); err != nil {
			return nil, fmt.Errorf("atomic catalog bundle is invalid: %w", err)
		}
		return files, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load atomic catalog bundle: %w", err)
	}

	// Compatibility path for caches created before atomic bundles existed.
	files := &CatalogFiles{}

	// Load models.yaml
	models, err := c.loadFile("models.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to load models: %w", err)
	}
	files.Models = models

	// Load providers.yaml
	providers, err := c.loadFile("providers.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to load providers: %w", err)
	}
	files.Providers = providers

	// Load version.txt
	version, err := c.loadFile("version.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to load version: %w", err)
	}
	files.Version = string(version)

	// Load changelog.yaml
	changelog, err := c.loadFile("changelog.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to load changelog: %w", err)
	}
	files.Changelog = changelog
	if err := catalogdistribution.ValidateCatalogDocuments(files.Models, files.Providers); err != nil {
		return nil, fmt.Errorf("legacy catalog cache is invalid: %w", err)
	}

	return files, nil
}

// GetCachedVersion gets the cached version
func (c *Cache) GetCachedVersion() (string, error) {
	files, err := c.LoadCatalog()
	if err != nil {
		return "", err
	}
	return files.Version, nil
}

// LoadChangelog loads only the durable changelog. It intentionally does not
// require the other cache files so notification reads remain independent.
func (c *Cache) LoadChangelog() ([]byte, error) {
	if files, err := c.LoadCatalog(); err == nil {
		return files.Changelog, nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.loadFile("changelog.yaml")
}

// GetMetadata gets cache metadata
func (c *Cache) GetMetadata() (*CacheMetadata, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	data, err := os.ReadFile(filepath.Join(c.cacheDir, "metadata.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return &CacheMetadata{}, nil
		}
		return nil, err
	}

	var metadata CacheMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, err
	}

	return &metadata, nil
}

// UpdateMetadata updates cache metadata
func (c *Cache) UpdateMetadata(metadata *CacheMetadata) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.saveMetadata(metadata)
}

// HasCache checks if cache exists
func (c *Cache) HasCache() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	bundleFile := filepath.Join(c.cacheDir, catalogBundleFilename)
	if _, err := os.Stat(bundleFile); err == nil {
		return true
	}
	versionFile := filepath.Join(c.cacheDir, "version.txt")
	_, err := os.Stat(versionFile)
	return err == nil
}

// saveFile saves a file to cache (atomic write)
func (c *Cache) saveFile(filename string, data []byte) error {
	filePath := filepath.Join(c.cacheDir, filename)

	// A unique temporary file keeps concurrent manual and scheduled refreshes
	// from sharing a staging path. Sync before rename so the promoted file has
	// durable contents.
	tempFile, err := os.CreateTemp(c.cacheDir, "."+filepath.Base(filename)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	if err := tempFile.Chmod(0o644); err != nil {
		_ = tempFile.Close()
		return err
	}
	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		return err
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, filePath)
}

// loadFile loads a file from cache
func (c *Cache) loadFile(filename string) ([]byte, error) {
	filePath := filepath.Join(c.cacheDir, filename)
	return os.ReadFile(filePath)
}

// saveMetadata saves cache metadata
func (c *Cache) saveMetadata(metadata *CacheMetadata) error {
	data, err := json.Marshal(metadata)
	if err != nil {
		return err
	}

	return c.saveFile("metadata.json", data)
}
