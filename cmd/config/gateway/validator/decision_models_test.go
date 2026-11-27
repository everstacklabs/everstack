package validator

import "testing"

func TestSelfHostedUnknownCatalogBudget(t *testing.T) {
	cfg := ModelsConfig{Providers: map[string]ProviderConfig{"kev": {Models: []DefaultModel{{Name: "kev-latest", MaxTokens: 0}}}}}
	req := ModelConfig{Provider: "kev", Model: []string{"kev-latest"}, MaxTokens: 4096}
	if err := validateModelAgainstDefaults(req, cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Providers["kev"] = ProviderConfig{Models: []DefaultModel{{Name: "kev-latest", MaxTokens: 1024}}}
	if err := validateModelAgainstDefaults(req, cfg); err == nil {
		t.Fatal("known limit not enforced")
	}
}
