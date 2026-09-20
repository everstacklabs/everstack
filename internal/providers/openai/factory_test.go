package openai

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/services/catalog"
)

func TestCatalogAPITypeLookupTracksHotRefresh(t *testing.T) {
	cache := catalog.NewCache()
	providers := []byte("providers:\n  openai:\n    name: openai\n")
	load := func(apiType string) {
		t.Helper()
		models := []byte("providers:\n  openai:\n    name: openai\n    models:\n      - name: gpt-6-astra\n        api_type: " + apiType + "\n")
		if err := cache.Refresh(models, providers); err != nil {
			t.Fatalf("Refresh() error = %v", err)
		}
	}

	load("responses")
	lookup := catalogAPITypeLookup(cache)
	if got := lookup("gpt-6-astra"); got != "responses" {
		t.Fatalf("lookup before refresh = %q, want responses", got)
	}

	load("chat_completions")
	if got := lookup("gpt-6-astra"); got != "chat_completions" {
		t.Fatalf("lookup after refresh = %q, want chat_completions", got)
	}
}

func TestCatalogAPITypeLookupAllowsNoCatalog(t *testing.T) {
	if lookup := catalogAPITypeLookup(nil); lookup != nil {
		t.Fatal("catalogAPITypeLookup(nil) must return nil")
	}
}
