package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Spec captures provider defaults we care about for bootstrapping connectors.
type Spec struct {
	Name       string `yaml:"name"`
	Display    string `yaml:"display_name"`
	BaseURL    string `yaml:"base_url"`
	APIVersion string `yaml:"api_version"`
}

type Catalog struct {
	byName map[string]Spec
}

// DefaultProvidersPath points to repository defaults; if missing at runtime,
// we'll fall back to a minimal in-memory catalog.
const DefaultProvidersPath = "cmd/config/gateway/defaults/providers.yaml"

func New() (*Catalog, error) {
	c := &Catalog{byName: make(map[string]Spec)}
	// Try repo-relative path
	if err := c.loadFromFile(DefaultProvidersPath); err != nil {
		// Attempt GOPATH/module relative
		_ = c.loadFromFile(filepath.Join("..", "..", DefaultProvidersPath))
	}
	if len(c.byName) == 0 {
		// Minimal fallback
		c.byName["openai"] = Spec{Name: "openai", Display: "OpenAI", BaseURL: "https://api.openai.com/v1"}
		c.byName["anthropic"] = Spec{Name: "anthropic", Display: "Anthropic", BaseURL: "https://api.anthropic.com/v1"}
	}
	return c, nil
}

func (c *Catalog) loadFromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return c.loadYAML(data)
}

func (c *Catalog) loadYAML(data []byte) error {
	// providers: map[string] {...}
	var raw struct {
		Providers map[string]struct {
			Name       string `yaml:"name"`
			Display    string `yaml:"display_name"`
			BaseURL    string `yaml:"base_url"`
			APIVersion string `yaml:"api_version"`
		} `yaml:"providers"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return err
	}
	for key, v := range raw.Providers {
		name := strings.ToLower(key)
		c.byName[name] = Spec{
			Name:       name,
			Display:    v.Display,
			BaseURL:    v.BaseURL,
			APIVersion: v.APIVersion,
		}
	}
	return nil
}

func (c *Catalog) Get(provider string) (Spec, bool) {
	if provider == "" {
		return Spec{}, false
	}
	s, ok := c.byName[strings.ToLower(provider)]
	return s, ok
}

func (c *Catalog) Require(provider string) (Spec, error) {
	if spec, ok := c.Get(provider); ok {
		return spec, nil
	}
	return Spec{}, errors.New("provider not found: " + provider)
}

// GetAllProviderNames returns a slice of all known provider names
func (c *Catalog) GetAllProviderNames() []string {
	names := make([]string, 0, len(c.byName))
	for name := range c.byName {
		names = append(names, name)
	}
	return names
}
