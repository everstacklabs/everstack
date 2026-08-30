package v1

import (
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/services/catalog_sync"
)

func TestConfiguredSyncIntervalKeepsFallbackForInvalidValues(t *testing.T) {
	t.Parallel()

	fallback := 24 * time.Hour
	for _, value := range []string{"", "invalid", "0s", "-1m"} {
		if got := configuredSyncInterval(value, fallback); got != fallback {
			t.Fatalf("configuredSyncInterval(%q) = %s, want %s", value, got, fallback)
		}
	}
	if got := configuredSyncInterval("5m", fallback); got != 5*time.Minute {
		t.Fatalf("configuredSyncInterval(5m) = %s", got)
	}
}

func TestBuildChangelogResponseUsesDurableVersionEntries(t *testing.T) {
	t.Parallel()

	changelog := &catalog_sync.Changelog{
		Versions: []catalog_sync.ChangelogVersion{
			{
				Version:     "2.4.0",
				Date:        "2026-07-27",
				Description: "Latest models",
				Changes: catalog_sync.ChangelogChanges{
					NewModels: []catalog_sync.ChangelogModelChange{
						{Provider: "anthropic", Model: "claude-opus-5", DisplayName: "Claude Opus 5"},
					},
				},
			},
			{Version: "2.3.0", Date: "2026-05-10"},
		},
	}

	response := buildChangelogResponse(changelog, "2.3.0")
	if len(response.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(response.Entries))
	}
	if got := response.Entries[0].NewModels[0]; got != "Anthropic · Claude Opus 5" {
		t.Fatalf("new model label = %q, want %q", got, "Anthropic · Claude Opus 5")
	}
}

func TestBuildNewModelsResponseFiltersByProvider(t *testing.T) {
	t.Parallel()

	changelog := &catalog_sync.Changelog{
		Versions: []catalog_sync.ChangelogVersion{
			{
				Version: "2.4.0",
				Changes: catalog_sync.ChangelogChanges{
					NewModels: []catalog_sync.ChangelogModelChange{
						{Provider: "anthropic", Model: "claude-opus-5", DisplayName: "Claude Opus 5"},
						{Provider: "openai", Model: "gpt-5.6", DisplayName: "GPT-5.6"},
					},
				},
			},
		},
	}

	response := buildNewModelsResponse(changelog, "openai")
	if response.TotalCount != 1 || len(response.Models) != 1 {
		t.Fatalf("models = %#v, want one OpenAI model", response.Models)
	}
	if got := response.Models[0].Provider; got != "openai" {
		t.Fatalf("provider = %q, want openai", got)
	}
	if got := response.Models[0].Name; got != "gpt-5.6" {
		t.Fatalf("model = %q, want gpt-5.6", got)
	}
}
