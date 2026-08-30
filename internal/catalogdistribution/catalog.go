package catalogdistribution

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type catalogModelsDocument struct {
	Providers map[string]struct {
		Models []map[string]interface{} `yaml:"models"`
	} `yaml:"providers"`
}

type catalogProvidersDocument struct {
	Providers map[string]map[string]interface{} `yaml:"providers"`
}

// ValidateCatalogDocuments rejects syntactically valid but empty, malformed, or
// internally inconsistent catalog payloads before they can become last known
// good state.
func ValidateCatalogDocuments(models, providers []byte) error {
	var modelDocument catalogModelsDocument
	if err := yaml.Unmarshal(models, &modelDocument); err != nil {
		return fmt.Errorf("decode catalog models: %w", err)
	}
	if len(modelDocument.Providers) == 0 {
		return fmt.Errorf("catalog models document has no providers")
	}

	modelCount := 0
	for providerName, provider := range modelDocument.Providers {
		if strings.TrimSpace(providerName) == "" {
			return fmt.Errorf("catalog models document has an empty provider name")
		}
		for _, model := range provider.Models {
			name, _ := model["name"].(string)
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("catalog provider %q contains a model without a name", providerName)
			}
			modelCount++
		}
	}
	if modelCount == 0 {
		return fmt.Errorf("catalog models document has no models")
	}

	var providerDocument catalogProvidersDocument
	if err := yaml.Unmarshal(providers, &providerDocument); err != nil {
		return fmt.Errorf("decode catalog providers: %w", err)
	}
	if len(providerDocument.Providers) == 0 {
		return fmt.Errorf("catalog providers document has no providers")
	}
	for providerName := range modelDocument.Providers {
		definition, ok := providerDocument.Providers[providerName]
		if !ok {
			return fmt.Errorf("catalog model provider %q has no provider definition", providerName)
		}
		if len(definition) == 0 {
			return fmt.Errorf("catalog provider %q has an empty definition", providerName)
		}
	}
	return nil
}
