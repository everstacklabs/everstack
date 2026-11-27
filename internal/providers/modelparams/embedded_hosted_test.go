package modelparams

import (
	"os"
	"path/filepath"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/services/catalog"
)

func embeddedCache(t *testing.T) *catalog.Cache {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "cmd", "config", "gateway", "defaults")
	modelsYAML, err := os.ReadFile(filepath.Join(dir, "models.yaml"))
	if err != nil {
		t.Fatalf("read embedded models.yaml: %v", err)
	}
	providersYAML, err := os.ReadFile(filepath.Join(dir, "providers.yaml"))
	if err != nil {
		t.Fatalf("read embedded providers.yaml: %v", err)
	}
	cache := catalog.NewCache()
	if err := cache.Load(modelsYAML, providersYAML); err != nil {
		t.Fatalf("load embedded catalog: %v", err)
	}
	return cache
}

// The hosted copies of GPT-6.2 and the new Claude models must drop the same
// request settings the first-party models reject. For the Bedrock OpenAI
// profiles there is no base entry to fall back to, so each profile file has
// to carry the list itself.
func TestEmbeddedCatalogHostedModelsDropRejectedSettings(t *testing.T) {
	cache := embeddedCache(t)
	tests := []struct {
		provider, model string
	}{
		{"aws-bedrock", "us.openai.gpt-6.2-sol"},
		{"aws-bedrock", "global.openai.gpt-6.2-sol"},
		{"aws-bedrock", "us.openai.gpt-6.2-astra"},
		{"aws-bedrock", "global.openai.gpt-6.2-astra"},
		{"azure-openai", "gpt-6.2-sol"},
		{"azure-openai", "gpt-6.2-astra"},
		{"azure-openai", "claude-haiku-5-5"},
		{"azure-openai", "claude-fable-5-2"},
	}
	for _, tt := range tests {
		req := gw.ChatCompletionRequest{Model: tt.model, Sampling: gw.SamplingParams{Temperature: 0.7, TopP: 0.9}}
		dropped := DropUnsupported(cache, tt.provider, &req)
		if req.Sampling.Temperature != 0 || req.Sampling.TopP != 0 {
			t.Errorf("%s/%s: temperature=%v top_p=%v after filter, want both cleared (dropped %v)", tt.provider, tt.model, req.Sampling.Temperature, req.Sampling.TopP, dropped)
		}
	}
}

// The Azure adapter picks its wire format from api_type, so the embedded
// entries must say which one each model needs.
func TestEmbeddedCatalogAzureAPITypes(t *testing.T) {
	cache := embeddedCache(t)
	want := map[string]string{
		"gpt-6.1-terra":    "responses",
		"gpt-6.2-sol":      "responses",
		"gpt-6.2-astra":    "responses",
		"claude-haiku-5-5": "anthropic",
		"claude-fable-5-2": "anthropic",
		"gpt-4o":           "",
	}
	for model, apiType := range want {
		definition, ok := cache.GetModel("azure-openai", model)
		if !ok {
			t.Errorf("azure-openai/%s missing from the embedded catalog", model)
			continue
		}
		if definition.APIType != apiType {
			t.Errorf("azure-openai/%s api_type = %q, want %q", model, definition.APIType, apiType)
		}
	}
}
