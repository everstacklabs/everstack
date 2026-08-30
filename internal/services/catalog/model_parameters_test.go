package catalog

import (
	"os"
	"testing"
)

// The gateway decides whether a provider-wide default may reach a model by
// asking the cache what that model accepts. That answer comes from a yaml tag
// on ModelDefinition, so this loads the embedded catalog the gateway actually
// ships with rather than a fixture: a silently dropped tag would otherwise
// make every model look like it accepts everything.
func TestCacheLoadsModelParametersFromEmbeddedCatalog(t *testing.T) {
	models, err := os.ReadFile("../../../cmd/config/gateway/defaults/models.yaml")
	if err != nil {
		t.Fatal(err)
	}
	providers, err := os.ReadFile("../../../cmd/config/gateway/defaults/providers.yaml")
	if err != nil {
		t.Fatal(err)
	}

	cache := NewCache()
	if err := cache.Load(models, providers); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	cases := []struct {
		provider string
		model    string
		key      string
		want     bool
	}{
		{"openai", "gpt-4.1", "temperature", true},
		{"openai", "gpt-4.1", "seed", true},
		// A reasoning model rejects the sampling window.
		{"openai", "gpt-5.2", "temperature", false},
		{"openai", "gpt-5.2", "reasoning_effort", true},
		{"openai", "gpt-5.2", "verbosity", true},
		{"anthropic", "claude-sonnet-4-5", "top_k", true},
		// Anthropic removed sampling on Opus 4.7 and later.
		{"anthropic", "claude-opus-5", "temperature", false},
	}

	for _, tc := range cases {
		model, ok := cache.GetModel(tc.provider, tc.model)
		if !ok {
			t.Errorf("%s/%s is not in the catalog", tc.provider, tc.model)
			continue
		}
		if got := model.SupportsParameter(tc.key); got != tc.want {
			t.Errorf("%s/%s SupportsParameter(%q) = %v, want %v",
				tc.provider, tc.model, tc.key, got, tc.want)
		}
	}
}
