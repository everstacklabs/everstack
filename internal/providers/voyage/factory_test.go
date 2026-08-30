package voyage

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/providers/factory"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

func TestVoyageRegistersWithTheProviderFactory(t *testing.T) {
	t.Parallel()

	build, ok := factory.Default.Get("voyage")
	if !ok {
		t.Fatal("voyage is not registered in the provider factory")
	}

	provider, err := build(factory.AggregatedInput{
		Provider: "voyage",
		APIKey:   "test-key",
		Models:   []string{"voyage-4"},
	})
	if err != nil {
		t.Fatalf("build voyage provider: %v", err)
	}
	if got := provider.Name(); got != "voyage" {
		t.Fatalf("provider name = %q, want %q", got, "voyage")
	}
}

// Voyage is an embeddings provider, so the built provider has to satisfy the
// embeddings capability or the catalog would advertise models the gateway
// cannot route.
func TestVoyageProviderServesEmbeddings(t *testing.T) {
	t.Parallel()

	build, _ := factory.Default.Get("voyage")
	provider, err := build(factory.AggregatedInput{
		Provider: "voyage",
		APIKey:   "test-key",
		Models:   []string{"voyage-4"},
	})
	if err != nil {
		t.Fatalf("build voyage provider: %v", err)
	}

	if _, ok := provider.(gw.EmbeddingsProvider); !ok {
		t.Fatal("voyage provider does not implement gw.EmbeddingsProvider")
	}
}
