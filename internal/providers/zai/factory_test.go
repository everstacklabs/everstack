package zai

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/providers/factory"
)

// A provider package that is never blank-imported registers nothing and fails
// silently at runtime, so pin that the init() actually lands in the registry.
func TestZAIRegistersWithTheProviderFactory(t *testing.T) {
	t.Parallel()

	build, ok := factory.Default.Get("zai")
	if !ok {
		t.Fatal("zai is not registered in the provider factory")
	}

	provider, err := build(factory.AggregatedInput{
		Provider: "zai",
		APIKey:   "test-key",
		Models:   []string{"glm-5.3"},
	})
	if err != nil {
		t.Fatalf("build zai provider: %v", err)
	}
	if provider == nil {
		t.Fatal("expected a provider instance")
	}
	if got := provider.Name(); got != "zai" {
		t.Fatalf("provider name = %q, want %q", got, "zai")
	}
}

// The registry lookup is case-insensitive, so a gateway.yaml naming the
// provider "Z.AI" or "ZAI" should still resolve.
func TestZAILookupIsCaseInsensitive(t *testing.T) {
	t.Parallel()

	if _, ok := factory.Default.Get("ZAI"); !ok {
		t.Fatal("uppercase ZAI did not resolve")
	}
}

func TestZAIRequiresAnAPIKey(t *testing.T) {
	t.Parallel()

	build, ok := factory.Default.Get("zai")
	if !ok {
		t.Fatal("zai is not registered in the provider factory")
	}

	if _, err := build(factory.AggregatedInput{Provider: "zai"}); err == nil {
		t.Fatal("expected an error when no API key is configured")
	}
}
