package validator

import (
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBuildLegacyModelsProviderPreservesReleaseDate(t *testing.T) {
	t.Parallel()

	provider := &CatalogProviderConfig{
		DisplayName: "Example",
		BaseURL:     "https://example.com/v1",
	}
	models := []CatalogModelConfig{
		{
			Name:           "example-latest",
			DisplayName:    "Example Latest",
			ReleaseDate:    "2026-07-24",
			AddedInVersion: "2.4.0",
			Status:         "stable",
		},
	}
	provider.Name = "openai"

	legacy := buildLegacyModelsProvider(provider, models)
	legacyModels, ok := legacy["models"].([]map[string]interface{})
	if !ok || len(legacyModels) != 1 {
		t.Fatalf("legacy models = %#v, want one model", legacy["models"])
	}

	if got := legacyModels[0]["release_date"]; got != "2026-07-24" {
		t.Fatalf("release_date = %#v, want %q", got, "2026-07-24")
	}
	if got := legacyModels[0]["added_in_version"]; got != "2.4.0" {
		t.Fatalf("added_in_version = %#v, want %q", got, "2.4.0")
	}
	if got := legacyModels[0]["publisher"]; got != "openai" {
		t.Fatalf("publisher = %#v, want %q", got, "openai")
	}
	if got := legacyModels[0]["canonical_model_id"]; got != "openai/example-latest" {
		t.Fatalf("canonical_model_id = %#v, want %q", got, "openai/example-latest")
	}
}

func TestBuildLegacyModelsProviderPreservesModelParameterMetadata(t *testing.T) {
	t.Parallel()

	provider := &CatalogProviderConfig{
		Name:        "openai",
		DisplayName: "OpenAI",
		BaseURL:     "https://api.openai.com/v1",
	}
	models := []CatalogModelConfig{
		{
			Name:        "gpt-5.6",
			DisplayName: "GPT-5.6",
			Limits: CatalogLimits{
				MaxTokens:           400000,
				MaxCompletionTokens: 128000,
			},
			Modalities: CatalogModalities{
				Input:  []string{"text", "image"},
				Output: []string{"text"},
			},
			StructuredOutput: true,
			Parameters: []ModelParameter{
				{
					Key:         "reasoning_effort",
					DisplayName: "Reasoning effort",
					Type:        "enum",
					Options:     []string{"low", "medium", "high", "xhigh"},
				},
			},
			Variants: []ModelVariant{
				{
					ID:          "xhigh",
					DisplayName: "Xhigh reasoning",
					Parameters:  map[string]string{"reasoning_effort": "xhigh"},
				},
			},
		},
	}

	legacy := buildLegacyModelsProvider(provider, models)
	legacyModels := legacy["models"].([]map[string]interface{})
	got := legacyModels[0]

	if got["max_output_tokens"] != 128000 {
		t.Fatalf("max_output_tokens = %#v, want 128000", got["max_output_tokens"])
	}
	if got["structured_output"] != true {
		t.Fatalf("structured_output = %#v, want true", got["structured_output"])
	}
	parameters, ok := got["parameters"].([]ModelParameter)
	if !ok || len(parameters) != 1 || parameters[0].Key != "reasoning_effort" {
		t.Fatalf("parameters = %#v, want reasoning_effort metadata", got["parameters"])
	}
	variants, ok := got["variants"].([]ModelVariant)
	if !ok || len(variants) != 1 || variants[0].Parameters["reasoning_effort"] != "xhigh" {
		t.Fatalf("variants = %#v, want xhigh preset", got["variants"])
	}
}

// Anthropic removed temperature, top_p and top_k on Claude Opus 4.7 and
// everything after it: sending any of them returns a 400. A catalog entry that
// advertises one lets a tenant save a default that breaks every request to
// that model, so a route that hands the request to Anthropic unchanged must
// never offer them. Aggregators in normalizingProviders drop parameters their
// upstream rejects, so the same controls are safe there and are kept for
// parity with what those gateways advertise. Model files are regenerated from
// models.dev, which does not model either rule, so the guard lives here rather
// than in review.
func TestCatalogOffersNoSamplingKnobsOnThinkingOnlyClaudeModels(t *testing.T) {
	models, _, err := LoadCatalogFromDirectory("../../../../model-catalog")
	if err != nil {
		t.Fatalf("LoadCatalogFromDirectory() error = %v", err)
	}

	var catalog struct {
		Providers map[string]struct {
			Models []struct {
				Name       string           `yaml:"name"`
				Parameters []ModelParameter `yaml:"parameters"`
			} `yaml:"models"`
		} `yaml:"providers"`
	}
	if err := yaml.Unmarshal(models, &catalog); err != nil {
		t.Fatalf("unmarshal aggregated catalog: %v", err)
	}

	thinkingOnly := regexp.MustCompile(`(?i)(opus-?4[-.]7|opus-?4[-.]8|opus-?5|sonnet-?5|fable-?5|mythos)`)
	rejected := map[string]struct{}{"temperature": {}, "top_p": {}, "top_k": {}}
	// Gateways that normalize a request against the upstream model's schema
	// before forwarding it, so an unsupported parameter is dropped rather than
	// rejected.
	normalizingProviders := map[string]struct{}{"openrouter": {}}

	for providerName, provider := range catalog.Providers {
		if _, normalizes := normalizingProviders[strings.ToLower(providerName)]; normalizes {
			continue
		}
		for _, model := range provider.Models {
			name := strings.ToLower(model.Name)
			if !strings.Contains(name, "claude") &&
				!strings.Contains(name, "fable") &&
				!strings.Contains(name, "mythos") {
				continue
			}
			if !thinkingOnly.MatchString(name) {
				continue
			}
			for _, parameter := range model.Parameters {
				if _, bad := rejected[parameter.Key]; bad {
					t.Errorf("%s/%s offers %q, which the model rejects with a 400",
						providerName, model.Name, parameter.Key)
				}
			}
		}
	}
}
