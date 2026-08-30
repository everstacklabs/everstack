package catalog_sync

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Changelog is the durable, versioned history shipped with the model catalog.
// Unlike merger state, it survives restarts and is identical across instances.
type Changelog struct {
	Versions []ChangelogVersion `yaml:"versions"`
}

type ChangelogVersion struct {
	Version     string           `yaml:"version"`
	Date        string           `yaml:"date"`
	Description string           `yaml:"description"`
	Changes     ChangelogChanges `yaml:"changes"`
}

type ChangelogChanges struct {
	NewModels        []ChangelogModelChange    `yaml:"new_models"`
	NewProviders     []ChangelogProviderChange `yaml:"new_providers"`
	UpdatedModels    []ChangelogModelChange    `yaml:"updated_models"`
	DeprecatedModels []ChangelogModelChange    `yaml:"deprecated_models"`
	PricingChanges   []string                  `yaml:"pricing_changes"`
}

type ChangelogModelChange struct {
	Provider    string `yaml:"provider"`
	Model       string `yaml:"model"`
	DisplayName string `yaml:"display_name"`
	Description string `yaml:"description"`
}

type ChangelogProviderChange struct {
	Name string `yaml:"name"`
}

func ParseChangelog(data []byte) (*Changelog, error) {
	if len(data) == 0 {
		return &Changelog{}, nil
	}

	var changelog Changelog
	if err := yaml.Unmarshal(data, &changelog); err != nil {
		return nil, fmt.Errorf("failed to parse catalog changelog: %w", err)
	}
	return &changelog, nil
}
