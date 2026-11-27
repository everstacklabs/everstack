package modelparams_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/internal/providers/modelparams"

	_ "github.com/everstacklabs/everstack/internal/providers/anthropic"
	_ "github.com/everstacklabs/everstack/internal/providers/aws_bedrock"
	_ "github.com/everstacklabs/everstack/internal/providers/azure_openai"
	_ "github.com/everstacklabs/everstack/internal/providers/baseten"
	_ "github.com/everstacklabs/everstack/internal/providers/cerebras"
	_ "github.com/everstacklabs/everstack/internal/providers/chutes"
	_ "github.com/everstacklabs/everstack/internal/providers/cohere"
	_ "github.com/everstacklabs/everstack/internal/providers/deepinfra"
	_ "github.com/everstacklabs/everstack/internal/providers/deepseek"
	_ "github.com/everstacklabs/everstack/internal/providers/fireworks"
	_ "github.com/everstacklabs/everstack/internal/providers/google"
	_ "github.com/everstacklabs/everstack/internal/providers/groq"
	_ "github.com/everstacklabs/everstack/internal/providers/huggingface"
	_ "github.com/everstacklabs/everstack/internal/providers/meta"
	_ "github.com/everstacklabs/everstack/internal/providers/minimax"
	_ "github.com/everstacklabs/everstack/internal/providers/mistral"
	_ "github.com/everstacklabs/everstack/internal/providers/moonshot"
	_ "github.com/everstacklabs/everstack/internal/providers/nebius"
	_ "github.com/everstacklabs/everstack/internal/providers/novita"
	_ "github.com/everstacklabs/everstack/internal/providers/nvidia_nim"
	_ "github.com/everstacklabs/everstack/internal/providers/ollama"
	_ "github.com/everstacklabs/everstack/internal/providers/openai"
	_ "github.com/everstacklabs/everstack/internal/providers/openrouter"
	_ "github.com/everstacklabs/everstack/internal/providers/perplexity"
	_ "github.com/everstacklabs/everstack/internal/providers/qwen"
	_ "github.com/everstacklabs/everstack/internal/providers/sambanova"
	_ "github.com/everstacklabs/everstack/internal/providers/systemone"
	_ "github.com/everstacklabs/everstack/internal/providers/tencent"
	_ "github.com/everstacklabs/everstack/internal/providers/together"
	_ "github.com/everstacklabs/everstack/internal/providers/vertex_ai"
	_ "github.com/everstacklabs/everstack/internal/providers/voyage"
	_ "github.com/everstacklabs/everstack/internal/providers/xai"
	_ "github.com/everstacklabs/everstack/internal/providers/zai"
)

// TestEveryRegisteredProviderNameMapsToItsCatalogKey builds each registered
// adapter and checks that the name the middleware looks up by (Name()) maps
// to the adapter's catalog provider directory. A mismatch means the catalog's
// unsupported_parameters can never apply to that provider's models.
func TestEveryRegisteredProviderNameMapsToItsCatalogKey(t *testing.T) {
	names := factory.Default.Names()
	if len(names) < 30 {
		t.Fatalf("registered providers = %d (%v), want the full set", len(names), names)
	}
	for _, registered := range names {
		t.Run(registered, func(t *testing.T) {
			build, _ := factory.Default.Get(registered)
			provider, err := build(factory.AggregatedInput{
				Provider: registered,
				APIKey:   "test-key",
				BaseURL:  "http://127.0.0.1:1",
			})
			if err != nil {
				t.Fatalf("build %s: %v", registered, err)
			}
			key := modelparams.CatalogProviderKey(provider.Name())
			if key != registered {
				t.Fatalf("Name() %q resolves to catalog key %q, want %q", provider.Name(), key, registered)
			}
			dir := filepath.Join("..", "..", "..", "model-catalog", "providers", key)
			if info, err := os.Stat(dir); err != nil || !info.IsDir() {
				t.Fatalf("catalog provider directory %s missing: %v", dir, err)
			}
		})
	}
}

// TestEveryCatalogProviderHasARegisteredFactory is the reverse check: a
// catalog provider directory with no registered factory is listed in the
// admin "add provider" flow and can be configured, but every request to its
// models fails with "no provider found for model". The catalog ships to
// gateways independently of the binary, so it must never name a provider the
// binary built from the same commit cannot construct.
func TestEveryCatalogProviderHasARegisteredFactory(t *testing.T) {
	root := filepath.Join("..", "..", "..", "model-catalog", "providers")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read catalog providers: %v", err)
	}
	registered := make(map[string]bool)
	for _, name := range factory.Default.Names() {
		registered[name] = true
	}
	checked := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		checked++
		if !registered[entry.Name()] {
			t.Errorf("catalog provider %q has no registered factory", entry.Name())
		}
	}
	if checked < 30 {
		t.Fatalf("checked %d catalog providers, want the full set", checked)
	}
}
