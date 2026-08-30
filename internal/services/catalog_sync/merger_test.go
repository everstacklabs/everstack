package catalog_sync

import (
	"reflect"
	"testing"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
)

func TestMergeCatalogsReplacesRemoteMetadataWithoutMutatingEmbeddedCatalog(t *testing.T) {
	t.Parallel()

	embedded := &validator.ModelsConfig{
		Providers: map[string]validator.ProviderConfig{
			"example": {
				Name: "Example",
				Models: []validator.DefaultModel{
					{
						Name:        "shared-model",
						DisplayName: "Shared Model",
						InputCost:   0.01,
						MaxTokens:   8_000,
					},
				},
			},
		},
	}
	remote := &CatalogFiles{
		Models: []byte(`
providers:
  example:
    name: Example
    models:
      - name: shared-model
        display_name: Shared Model
        input_cost_per_1k: 0.002
        max_tokens: 1000000
`),
		Providers: []byte("providers:\n  example:\n    display_name: Example\n"),
	}

	result, err := NewMerger(embedded, map[string]interface{}{}).MergeCatalogs(remote)
	if err != nil {
		t.Fatalf("MergeCatalogs() error = %v", err)
	}

	got := result.Models.Providers["example"].Models[0]
	if got.InputCost != 0.002 || got.MaxTokens != 1_000_000 {
		t.Fatalf("merged model = %#v, want remote pricing and limits", got)
	}

	original := embedded.Providers["example"].Models[0]
	if original.InputCost != 0.01 || original.MaxTokens != 8_000 {
		t.Fatalf("embedded model was mutated: %#v", original)
	}

	if want := []string{"example/shared-model"}; !reflect.DeepEqual(result.UpdatedModels, want) {
		t.Fatalf("updated models = %v, want %v", result.UpdatedModels, want)
	}
}

func TestMergeCatalogsScopesDuplicateModelNamesByProvider(t *testing.T) {
	t.Parallel()

	embedded := &validator.ModelsConfig{
		Providers: map[string]validator.ProviderConfig{
			"provider-a": {
				Models: []validator.DefaultModel{{Name: "shared-model"}},
			},
		},
	}
	remote := &CatalogFiles{
		Models: []byte(`
providers:
  provider-b:
    name: Provider B
    models:
      - name: shared-model
        display_name: Shared Model
`),
		Providers: []byte("providers:\n  provider-b:\n    display_name: Provider B\n"),
	}

	result, err := NewMerger(embedded, map[string]interface{}{}).MergeCatalogs(remote)
	if err != nil {
		t.Fatalf("MergeCatalogs() error = %v", err)
	}

	if want := []string{"provider-b/shared-model"}; !reflect.DeepEqual(result.NewModels, want) {
		t.Fatalf("new models = %v, want %v", result.NewModels, want)
	}
}
