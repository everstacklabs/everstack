package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"gopkg.in/yaml.v3"
)

// writeFixtureCatalog lays out a one-provider hierarchical catalog in a temp
// directory, in the same shape as model-catalog/.
func writeFixtureCatalog(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	modelsDir := filepath.Join(root, "providers", "anthropic", "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(root, "providers", "anthropic", "provider.yaml"): `name: anthropic
display_name: Anthropic
base_url: https://api.anthropic.com/v1
`,
		filepath.Join(modelsDir, "claude-fable-5-2.yaml"): `name: claude-fable-5-2
display_name: Claude Fable 5.2
status: stable
unsupported_parameters:
  - temperature
  - top_p
  - top_k
  - thinking_toggle
  - thinking_budget
`,
		filepath.Join(modelsDir, "claude-opus-4-8.yaml"): `name: claude-opus-4-8
display_name: Claude Opus 4.8
status: stable
`,
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

var wantFable52Unsupported = []string{"temperature", "top_p", "top_k", "thinking_toggle", "thinking_budget"}

// The path a catalog author's edit takes to a running gateway: hierarchical
// YAML -> LoadCatalogFromDirectory (what generate-embedded-defaults.go and
// tools/catalog-release write) -> embedded models.yaml -> catalog cache.
func TestUnsupportedParametersSurviveLoaderToCacheRoundTrip(t *testing.T) {
	modelsYAML, providersYAML, err := validator.LoadCatalogFromDirectory(writeFixtureCatalog(t))
	if err != nil {
		t.Fatalf("LoadCatalogFromDirectory() error = %v", err)
	}

	// Embedded defaults as the validator reads them.
	defaults, err := validator.ParseModelsDefaults(modelsYAML)
	if err != nil {
		t.Fatalf("ParseModelsDefaults() error = %v", err)
	}
	byName := map[string]validator.DefaultModel{}
	for _, model := range defaults.Providers["anthropic"].Models {
		byName[model.Name] = model
	}
	if got := byName["claude-fable-5-2"].UnsupportedParameters; !reflect.DeepEqual(got, wantFable52Unsupported) {
		t.Fatalf("defaults unsupported_parameters = %v, want %v", got, wantFable52Unsupported)
	}
	if got := byName["claude-opus-4-8"].UnsupportedParameters; len(got) != 0 {
		t.Fatalf("opus unsupported_parameters = %v, want none", got)
	}

	// A model without the field must serialise exactly as before: no key.
	if strings.Count(string(modelsYAML), "unsupported_parameters") != 1 {
		t.Fatalf("unsupported_parameters emitted for a model that does not declare it:\n%s", modelsYAML)
	}

	// The runtime cache.
	cache := NewCache()
	if err := cache.Load(modelsYAML, providersYAML); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	model, ok := cache.ResolveModel("anthropic", "claude-fable-5-2")
	if !ok {
		t.Fatal("claude-fable-5-2 missing from cache")
	}
	if !reflect.DeepEqual(model.UnsupportedParameters, wantFable52Unsupported) {
		t.Fatalf("cache unsupported_parameters = %v, want %v", model.UnsupportedParameters, wantFable52Unsupported)
	}
	for _, key := range wantFable52Unsupported {
		if !model.RejectsParameter(key) {
			t.Errorf("RejectsParameter(%q) = false", key)
		}
	}
	if model.RejectsParameter("reasoning_effort") {
		t.Error("RejectsParameter(reasoning_effort) = true, want false")
	}
}

// The database hop stores the field in the config JSONB column, so the list
// comes back as []interface{}.
func TestUnsupportedParametersSurviveDatabaseRoundTrip(t *testing.T) {
	definition := ModelDefinition{Name: "gpt-6.2-sol", UnsupportedParameters: []string{"temperature", "top_p"}}
	encoded, err := json.Marshal(modelConfig(definition))
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	var stored map[string]interface{}
	if err := json.Unmarshal(encoded, &stored); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if _, ok := stored["unsupported_parameters"].([]interface{}); !ok {
		t.Fatalf("stored config = %#v, want a JSON list", stored["unsupported_parameters"])
	}
	if _, present := modelConfig(ModelDefinition{Name: "gpt-4o"})["unsupported_parameters"]; present {
		t.Fatal("modelConfig() stores unsupported_parameters for a model that declares none")
	}

	modelsYAML, err := convertModelsToYAML([]*CatalogModel{
		{ProviderName: "openai", ModelName: "gpt-6.2-sol", Config: stored},
		{ProviderName: "openai", ModelName: "gpt-4o", Config: map[string]interface{}{}},
	})
	if err != nil {
		t.Fatalf("convertModelsToYAML() error = %v", err)
	}
	cache := NewCache()
	if err := cache.Load(modelsYAML, []byte("providers:\n  openai:\n    name: openai\n")); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	model, _ := cache.GetModel("openai", "gpt-6.2-sol")
	if model == nil || !reflect.DeepEqual(model.UnsupportedParameters, []string{"temperature", "top_p"}) {
		t.Fatalf("cache model = %#v, want unsupported temperature and top_p", model)
	}
	plain, _ := cache.GetModel("openai", "gpt-4o")
	if plain == nil || plain.UnsupportedParameters != nil {
		t.Fatalf("plain model = %#v, want no unsupported parameters", plain)
	}
}

// The remote (non-signed, legacy) sync path rebuilds the legacy document from
// per-model YAML maps.
func TestRemoteLegacyConversionKeepsUnsupportedParameters(t *testing.T) {
	legacy := convertModelToLegacy("anthropic", map[string]interface{}{
		"name":                   "claude-haiku-5-5",
		"unsupported_parameters": []interface{}{"temperature", "thinking_budget"},
	})
	if got := legacy["unsupported_parameters"]; !reflect.DeepEqual(got, []interface{}{"temperature", "thinking_budget"}) {
		t.Fatalf("unsupported_parameters = %#v", got)
	}
	if _, present := convertModelToLegacy("anthropic", map[string]interface{}{"name": "x"})["unsupported_parameters"]; present {
		t.Fatal("legacy conversion invented unsupported_parameters")
	}
}

// An older gateway's model struct has no field for the new key. Decoding a
// catalog that carries it must still succeed, which is what keeps released
// gateways working against a newer catalog.
func TestOlderModelShapeIgnoresUnsupportedParametersKey(t *testing.T) {
	modelsYAML := []byte(`providers:
  anthropic:
    name: anthropic
    models:
      - name: claude-fable-5-2
        unsupported_parameters: [temperature]
`)
	var older struct {
		Providers map[string]struct {
			Models []struct {
				Name string `yaml:"name"`
			} `yaml:"models"`
		} `yaml:"providers"`
	}
	if err := yaml.Unmarshal(modelsYAML, &older); err != nil {
		t.Fatalf("older shape failed to decode: %v", err)
	}
	if _, err := validator.ParseModelsDefaults(modelsYAML); err != nil {
		t.Fatalf("ParseModelsDefaults() error = %v", err)
	}
}

func TestResolveModelStripsVariantSuffix(t *testing.T) {
	cache := NewCache()
	if err := cache.Load([]byte(`providers:
  anthropic:
    models:
      - name: claude-opus-4-8
      - name: claude-fable-5
      - name: claude-fable-5-2
`), []byte("providers:\n  anthropic:\n    name: anthropic\n")); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"claude-opus-4-8", "claude-opus-4-8[1m]", "CLAUDE-OPUS-4-8-20261101"} {
		if model, ok := cache.ResolveModel("anthropic", id); !ok || model.Name != "claude-opus-4-8" {
			t.Errorf("ResolveModel(%q) = %v, %v", id, model, ok)
		}
	}
	// The variant marker is stripped before any prefix match, so a longer
	// model id never falls back onto a shorter sibling.
	for id, want := range map[string]string{
		"claude-fable-5-2[1m]":      "claude-fable-5-2",
		"claude-fable-5-2-20261101": "claude-fable-5-2",
		"claude-fable-5[1m]":        "claude-fable-5",
	} {
		if model, ok := cache.ResolveModel("anthropic", id); !ok || model.Name != want {
			t.Errorf("ResolveModel(%q) = %v, %v, want %s", id, model, ok, want)
		}
	}
	if _, ok := cache.ResolveModel("anthropic", "claude-opus-4-9"); ok {
		t.Error("ResolveModel matched an unrelated model")
	}
	var nilCache *Cache
	if _, ok := nilCache.ResolveModel("anthropic", "claude-opus-4-8"); ok {
		t.Error("nil cache resolved a model")
	}
}
