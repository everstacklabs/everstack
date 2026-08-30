package catalog_sync

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
)

// Merger handles merging embedded and remote catalogs
type Merger struct {
	embeddedModels    *validator.ModelsConfig
	embeddedProviders map[string]interface{}
}

// NewMerger creates a new catalog merger
func NewMerger(embeddedModels *validator.ModelsConfig, embeddedProviders map[string]interface{}) *Merger {
	return &Merger{
		embeddedModels:    embeddedModels,
		embeddedProviders: embeddedProviders,
	}
}

// MergeResult represents the result of merging catalogs
type MergeResult struct {
	Models           *validator.ModelsConfig
	Providers        map[string]interface{}
	NewModels        []string
	NewProviders     []string
	UpdatedModels    []string
	DeprecatedModels []string
}

// MergeCatalogs merges embedded and remote catalogs
func (m *Merger) MergeCatalogs(remoteFiles *CatalogFiles) (*MergeResult, error) {
	result := &MergeResult{
		NewModels:        []string{},
		NewProviders:     []string{},
		UpdatedModels:    []string{},
		DeprecatedModels: []string{},
	}

	// Parse remote models
	remoteModels, err := validator.ParseModelsDefaults(remoteFiles.Models)
	if err != nil {
		return nil, fmt.Errorf("failed to parse remote models: %w", err)
	}

	// Work on a copy: the embedded catalog is the immutable fallback baseline.
	result.Models = cloneModelsConfig(m.embeddedModels)

	// Merge remote models into embedded
	updatedModels, deprecatedModels, err := m.mergeModels(result.Models, remoteModels)
	if err != nil {
		return nil, fmt.Errorf("failed to merge models: %w", err)
	}
	result.UpdatedModels = updatedModels
	result.DeprecatedModels = deprecatedModels

	// Detect new models
	result.NewModels = m.detectNewModels(remoteModels)

	// Parse remote providers
	var remoteProviders map[string]interface{}
	if len(remoteFiles.Providers) > 0 {
		if err := validator.LoadYAMLIntoStruct(remoteFiles.Providers, &remoteProviders); err != nil {
			return nil, fmt.Errorf("failed to parse remote providers: %w", err)
		}
	}

	// Merge providers - start with embedded as base, then add/update from remote
	result.Providers = make(map[string]interface{})

	// Handle case where YAML has top-level "providers" key
	embeddedProvidersMap := m.embeddedProviders
	if providersKey, ok := m.embeddedProviders["providers"].(map[string]interface{}); ok {
		// Embedded has "providers" key, use it directly
		result.Providers["providers"] = make(map[string]interface{})
		for k, v := range providersKey {
			result.Providers["providers"].(map[string]interface{})[k] = cloneValue(v)
		}
	} else {
		// No "providers" key, copy directly
		for k, v := range embeddedProvidersMap {
			result.Providers[k] = cloneValue(v)
		}
	}

	// Merge remote providers
	if remoteProvidersKey, ok := remoteProviders["providers"].(map[string]interface{}); ok {
		// Remote has "providers" key
		if _, hasProvidersKey := result.Providers["providers"]; !hasProvidersKey {
			result.Providers["providers"] = make(map[string]interface{})
		}
		for k, v := range remoteProvidersKey {
			result.Providers["providers"].(map[string]interface{})[k] = cloneValue(v)
		}
	} else {
		// No "providers" key, copy directly
		for k, v := range remoteProviders {
			result.Providers[k] = cloneValue(v)
		}
	}

	// Detect new providers
	result.NewProviders = m.detectNewProviders(remoteProviders)
	sort.Strings(result.NewModels)
	sort.Strings(result.NewProviders)
	sort.Strings(result.UpdatedModels)
	sort.Strings(result.DeprecatedModels)

	return result, nil
}

// mergeModels merges remote models into embedded models
func (m *Merger) mergeModels(embedded, remote *validator.ModelsConfig) ([]string, []string, error) {
	// Initialize Providers map if nil (happens when embedded is empty)
	if embedded.Providers == nil {
		embedded.Providers = make(map[string]validator.ProviderConfig)
	}

	var updatedModels []string
	var deprecatedModels []string

	// For each provider in remote
	for providerName, remoteProvider := range remote.Providers {
		// Check if provider exists in embedded
		if embeddedProvider, exists := embedded.Providers[providerName]; exists {
			// Merge models for existing provider
			updated, deprecated := m.mergeProviderModels(providerName, &embeddedProvider, &remoteProvider)
			updatedModels = append(updatedModels, updated...)
			deprecatedModels = append(deprecatedModels, deprecated...)
			embedded.Providers[providerName] = embeddedProvider
		} else {
			embedded.Providers[providerName] = cloneProviderConfig(remoteProvider)
		}
	}

	return updatedModels, deprecatedModels, nil
}

// mergeProviderModels merges models for a specific provider
func (m *Merger) mergeProviderModels(providerName string, embedded, remote *validator.ProviderConfig) ([]string, []string) {
	existingModels := make(map[string]validator.DefaultModel, len(embedded.Models))
	for _, model := range embedded.Models {
		existingModels[model.Name] = model
	}

	mergedModels := make([]validator.DefaultModel, 0, len(remote.Models)+len(embedded.Models))
	remoteNames := make(map[string]bool, len(remote.Models))
	var updatedModels []string
	var deprecatedModels []string

	for _, remoteModel := range remote.Models {
		remoteNames[remoteModel.Name] = true
		if existingModel, exists := existingModels[remoteModel.Name]; exists {
			if !reflect.DeepEqual(existingModel, remoteModel) {
				qualifiedName := providerName + "/" + remoteModel.Name
				updatedModels = append(updatedModels, qualifiedName)
				if existingModel.Status != "deprecated" && remoteModel.Status == "deprecated" {
					deprecatedModels = append(deprecatedModels, qualifiedName)
				}
			}
		}
		mergedModels = append(mergedModels, cloneDefaultModel(remoteModel))
	}

	// Preserve embedded-only models as an offline/backward-compatible fallback.
	for _, embeddedModel := range embedded.Models {
		if !remoteNames[embeddedModel.Name] {
			mergedModels = append(mergedModels, cloneDefaultModel(embeddedModel))
		}
	}

	mergedProvider := cloneProviderConfig(*remote)
	mergedProvider.Models = mergedModels
	*embedded = mergedProvider
	return updatedModels, deprecatedModels
}

// detectNewModels detects models that are new (not in embedded)
func (m *Merger) detectNewModels(remote *validator.ModelsConfig) []string {
	var newModels []string

	// Create a map of embedded models for quick lookup
	embeddedModels := make(map[string]bool)
	if m.embeddedModels != nil && m.embeddedModels.Providers != nil {
		for providerName, provider := range m.embeddedModels.Providers {
			for _, model := range provider.Models {
				embeddedModels[providerName+"/"+model.Name] = true
			}
		}
	}

	// Check remote models against embedded
	if remote != nil && remote.Providers != nil {
		for providerName, provider := range remote.Providers {
			for _, model := range provider.Models {
				qualifiedName := providerName + "/" + model.Name
				if !embeddedModels[qualifiedName] {
					newModels = append(newModels, qualifiedName)
				}
			}
		}
	}

	return newModels
}

func cloneModelsConfig(source *validator.ModelsConfig) *validator.ModelsConfig {
	cloned := &validator.ModelsConfig{
		Providers: make(map[string]validator.ProviderConfig),
	}
	if source == nil {
		return cloned
	}
	for name, provider := range source.Providers {
		cloned.Providers[name] = cloneProviderConfig(provider)
	}
	return cloned
}

func cloneProviderConfig(source validator.ProviderConfig) validator.ProviderConfig {
	cloned := source
	cloned.Models = make([]validator.DefaultModel, 0, len(source.Models))
	for _, model := range source.Models {
		cloned.Models = append(cloned.Models, cloneDefaultModel(model))
	}
	return cloned
}

func cloneDefaultModel(source validator.DefaultModel) validator.DefaultModel {
	cloned := source
	cloned.Capabilities = append([]string(nil), source.Capabilities...)
	return cloned
}

func cloneValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		cloned := make(map[string]interface{}, len(typed))
		for key, child := range typed {
			cloned[key] = cloneValue(child)
		}
		return cloned
	case []interface{}:
		cloned := make([]interface{}, len(typed))
		for index, child := range typed {
			cloned[index] = cloneValue(child)
		}
		return cloned
	default:
		return value
	}
}

// detectNewProviders detects providers that are new (not in embedded)
func (m *Merger) detectNewProviders(remote map[string]interface{}) []string {
	var newProviders []string

	// Extract embedded providers map (handle "providers" key if present)
	embeddedProvidersMap := m.embeddedProviders
	if m.embeddedProviders != nil {
		if providersKey, ok := m.embeddedProviders["providers"].(map[string]interface{}); ok {
			embeddedProvidersMap = providersKey
		}
	}

	// Extract remote providers map (handle "providers" key if present)
	remoteProvidersMap := remote
	if remote != nil {
		if providersKey, ok := remote["providers"].(map[string]interface{}); ok {
			remoteProvidersMap = providersKey
		}
	}

	// Create a map of embedded providers for quick lookup
	embeddedProviders := make(map[string]bool)
	if embeddedProvidersMap != nil {
		for providerName := range embeddedProvidersMap {
			embeddedProviders[strings.ToLower(providerName)] = true
		}
	}

	// Check remote providers against embedded
	if remoteProvidersMap != nil {
		for providerName := range remoteProvidersMap {
			if !embeddedProviders[strings.ToLower(providerName)] {
				newProviders = append(newProviders, providerName)
			}
		}
	}

	return newProviders
}

// Helper function to get keys from a map
// func getKeys(m map[string]interface{}) []string {
// 	keys := make([]string, 0, len(m))
// 	for k := range m {
// 		keys = append(keys, k)
// 	}
// 	return keys
// }
