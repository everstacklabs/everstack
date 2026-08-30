package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/catalogdistribution"
	"gopkg.in/yaml.v3"
)

type catalogSource struct {
	version   string
	models    []byte
	providers []byte
	changelog []byte
}

type sourceChangelog struct {
	Versions []sourceChangelogVersion `yaml:"versions"`
}

type sourceChangelogVersion struct {
	Version     string                  `yaml:"version"`
	Date        string                  `yaml:"date"`
	Description string                  `yaml:"description"`
	Changes     *sourceChangelogChanges `yaml:"changes"`
}

type sourceChangelogChanges struct {
	NewModels []sourceChangelogModel `yaml:"new_models"`
}

type sourceChangelogModel struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}

func loadCatalogSource(catalogDir string) (*catalogSource, error) {
	versionData, err := os.ReadFile(filepath.Join(catalogDir, "version.txt"))
	if err != nil {
		return nil, fmt.Errorf("read catalog version: %w", err)
	}
	version := strings.TrimSpace(string(versionData))
	if _, err := catalogdistribution.CompareVersions("", version); err != nil {
		return nil, err
	}

	manifest, err := validator.LoadCatalogManifest(catalogDir)
	if err != nil {
		return nil, fmt.Errorf("read catalog manifest: %w", err)
	}
	if manifest.Version != version {
		return nil, fmt.Errorf("catalog manifest version is %q, version.txt is %q", manifest.Version, version)
	}
	if err := validateManifestCoverage(catalogDir, manifest); err != nil {
		return nil, err
	}

	changelog, err := os.ReadFile(filepath.Join(catalogDir, "changelog.yaml"))
	if err != nil {
		return nil, fmt.Errorf("read catalog changelog: %w", err)
	}
	latestChangelog, err := validateSourceChangelog(changelog, version)
	if err != nil {
		return nil, err
	}

	models, providers, err := validator.LoadCatalogFromDirectory(catalogDir)
	if err != nil {
		return nil, fmt.Errorf("build aggregated catalog: %w", err)
	}
	if err := catalogdistribution.ValidateCatalogDocuments(models, providers); err != nil {
		return nil, fmt.Errorf("validate aggregated catalog: %w", err)
	}
	if err := validateLatestChangelogModels(models, latestChangelog); err != nil {
		return nil, err
	}

	return &catalogSource{
		version:   version,
		models:    models,
		providers: providers,
		changelog: changelog,
	}, nil
}

func validateSourceChangelog(data []byte, catalogVersion string) (*sourceChangelogVersion, error) {
	var changelog sourceChangelog
	if err := yaml.Unmarshal(data, &changelog); err != nil {
		return nil, fmt.Errorf("parse catalog changelog: %w", err)
	}
	if len(changelog.Versions) == 0 {
		return nil, fmt.Errorf("catalog changelog has no versions")
	}

	seen := make(map[string]struct{}, len(changelog.Versions))
	for index := range changelog.Versions {
		entry := &changelog.Versions[index]
		entry.Version = strings.TrimSpace(entry.Version)
		if _, err := catalogdistribution.CompareVersions("", entry.Version); err != nil {
			return nil, fmt.Errorf("catalog changelog entry %d: %w", index+1, err)
		}
		if _, duplicate := seen[entry.Version]; duplicate {
			return nil, fmt.Errorf("catalog changelog version %q is listed more than once", entry.Version)
		}
		seen[entry.Version] = struct{}{}
		if _, err := time.Parse("2006-01-02", strings.TrimSpace(entry.Date)); err != nil {
			return nil, fmt.Errorf("catalog changelog version %q has invalid date %q", entry.Version, entry.Date)
		}
		if strings.TrimSpace(entry.Description) == "" {
			return nil, fmt.Errorf("catalog changelog version %q has no description", entry.Version)
		}
		if entry.Changes == nil {
			return nil, fmt.Errorf("catalog changelog version %q has no changes", entry.Version)
		}
		if index > 0 {
			comparison, err := catalogdistribution.CompareVersions(entry.Version, changelog.Versions[index-1].Version)
			if err != nil {
				return nil, fmt.Errorf("compare catalog changelog versions: %w", err)
			}
			if comparison <= 0 {
				return nil, fmt.Errorf("catalog changelog versions are not newest first at %q", entry.Version)
			}
		}
	}
	if changelog.Versions[0].Version != catalogVersion {
		return nil, fmt.Errorf("catalog changelog latest version must be %q", catalogVersion)
	}
	return &changelog.Versions[0], nil
}

func validateLatestChangelogModels(models []byte, latest *sourceChangelogVersion) error {
	var catalog struct {
		Providers map[string]struct {
			Models []struct {
				Name string `yaml:"name"`
			} `yaml:"models"`
		} `yaml:"providers"`
	}
	if err := yaml.Unmarshal(models, &catalog); err != nil {
		return fmt.Errorf("decode aggregated catalog for changelog validation: %w", err)
	}

	available := make(map[string]struct{})
	for providerName, provider := range catalog.Providers {
		for _, model := range provider.Models {
			available[providerName+"/"+model.Name] = struct{}{}
		}
	}
	listed := make(map[string]struct{}, len(latest.Changes.NewModels))
	for _, model := range latest.Changes.NewModels {
		key := strings.TrimSpace(model.Provider) + "/" + strings.TrimSpace(model.Model)
		if _, duplicate := listed[key]; duplicate {
			return fmt.Errorf("latest changelog model %q is listed more than once", key)
		}
		listed[key] = struct{}{}
		if _, ok := available[key]; !ok {
			return fmt.Errorf("latest changelog model %q does not exist in the catalog", key)
		}
	}
	return nil
}

func validateManifestCoverage(catalogDir string, manifest *validator.CatalogManifest) error {
	recorded := make(map[string]validator.ManifestProvider, len(manifest.Providers))
	for _, provider := range manifest.Providers {
		if _, duplicate := recorded[provider.Name]; duplicate {
			return fmt.Errorf("catalog manifest is stale: provider %q is listed more than once", provider.Name)
		}
		recorded[provider.Name] = provider
	}

	providerEntries, err := os.ReadDir(filepath.Join(catalogDir, "providers"))
	if err != nil {
		return fmt.Errorf("read catalog providers: %w", err)
	}
	actualProviderCount := 0
	totalModels := 0
	staticProviders := 0
	metaProviders := 0
	for _, entry := range providerEntries {
		if !entry.IsDir() {
			continue
		}
		actualProviderCount++
		providerName := entry.Name()
		provider, ok := recorded[providerName]
		if !ok {
			return fmt.Errorf("catalog manifest is stale: provider %q is missing", providerName)
		}

		providerDir := filepath.Join(catalogDir, "providers", providerName)
		providerData, err := os.ReadFile(filepath.Join(providerDir, "provider.yaml"))
		if err != nil {
			return fmt.Errorf("catalog manifest is stale: provider %q has no provider.yaml", providerName)
		}
		var providerType struct {
			Type string `yaml:"provider_type"`
		}
		if err := yaml.Unmarshal(providerData, &providerType); err != nil {
			return fmt.Errorf("parse provider type for %q: %w", providerName, err)
		}
		if providerType.Type == "meta" {
			metaProviders++
		} else {
			staticProviders++
		}

		var expectedFiles []string
		for _, name := range []string{"provider.yaml", "categories.yaml", "templates.yaml"} {
			if info, err := os.Stat(filepath.Join(providerDir, name)); err == nil && !info.IsDir() {
				expectedFiles = append(expectedFiles, filepath.ToSlash(filepath.Join("providers", providerName, name)))
			}
		}

		var expectedModels []string
		modelEntries, err := os.ReadDir(filepath.Join(providerDir, "models"))
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("read catalog models for %q: %w", providerName, err)
		}
		for _, model := range modelEntries {
			if !model.IsDir() && strings.HasSuffix(model.Name(), ".yaml") {
				expectedModels = append(expectedModels, filepath.ToSlash(filepath.Join("providers", providerName, "models", model.Name())))
			}
		}
		totalModels += len(expectedModels)

		if !equalStringSets(provider.Files, expectedFiles) || !equalStringSets(provider.Models, expectedModels) {
			return fmt.Errorf("catalog manifest is stale: provider %q file inventory does not match the catalog", providerName)
		}
	}
	if len(recorded) != actualProviderCount {
		return fmt.Errorf("catalog manifest is stale: provider inventory does not match the catalog")
	}
	expectedStats := validator.ManifestStats{
		TotalProviders:  actualProviderCount,
		TotalModels:     totalModels,
		StaticProviders: staticProviders,
		MetaProviders:   metaProviders,
	}
	if manifest.Stats != expectedStats {
		return fmt.Errorf("catalog manifest is stale: statistics do not match the catalog")
	}
	return nil
}

func equalStringSets(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
