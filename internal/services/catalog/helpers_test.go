package catalog

import "testing"

func TestConvertModelsToYAMLPreservesRoutingAndTieredPricing(t *testing.T) {
	modelsYAML, err := convertModelsToYAML([]*CatalogModel{{
		ProviderName:    "openai",
		ModelName:       "gpt-6-astra",
		DisplayName:     "GPT-6 Astra",
		InputCostPer1k:  0.01,
		OutputCostPer1k: 0.05,
		Config: map[string]interface{}{
			"api_type":                       "responses",
			"cache_write_1h_cost_per_1k":     0.02,
			"long_context_threshold_tokens":  float64(272000),
			"long_context_input_multiplier":  2.0,
			"long_context_cache_multiplier":  2.0,
			"long_context_output_multiplier": 1.5,
		},
	}})
	if err != nil {
		t.Fatalf("convertModelsToYAML() error = %v", err)
	}

	cache := NewCache()
	providersYAML := []byte("providers:\n  openai:\n    name: openai\n")
	if err := cache.Load(modelsYAML, providersYAML); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	model, ok := cache.GetModel("openai", "gpt-6-astra")
	if !ok {
		t.Fatal("gpt-6-astra missing after database-shaped YAML conversion")
	}
	if model.APIType != "responses" || model.CacheWrite1hCostPer1k != 0.02 {
		t.Fatalf("routing/cache pricing = %q/%g, want responses/.02", model.APIType, model.CacheWrite1hCostPer1k)
	}
	if model.LongContextThresholdTokens != 272000 || model.LongContextInputMultiplier != 2 ||
		model.LongContextCacheMultiplier != 2 || model.LongContextOutputMultiplier != 1.5 {
		t.Fatalf("long-context pricing = %#v", model)
	}
}
