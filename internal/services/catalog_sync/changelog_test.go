package catalog_sync

import "testing"

func TestParseChangelogPreservesStructuredModelChanges(t *testing.T) {
	t.Parallel()

	data := []byte(`
versions:
  - version: "2.4.0"
    date: "2026-07-27"
    description: "Latest models"
    changes:
      new_models:
        - provider: anthropic
          model: claude-opus-5
          display_name: "Claude Opus 5"
      new_providers:
        - name: example-ai
      updated_models:
        - provider: openai
          model: gpt-5.6
          description: "Updated pricing"
      deprecated_models:
        - provider: openai
          model: gpt-4
          description: "Legacy"
      pricing_changes:
        - "OpenAI pricing refreshed"
`)

	changelog, err := ParseChangelog(data)
	if err != nil {
		t.Fatalf("ParseChangelog() error = %v", err)
	}
	if len(changelog.Versions) != 1 {
		t.Fatalf("versions = %d, want 1", len(changelog.Versions))
	}

	entry := changelog.Versions[0]
	if got := entry.Changes.NewModels[0].Provider; got != "anthropic" {
		t.Fatalf("new model provider = %q, want anthropic", got)
	}
	if got := entry.Changes.NewModels[0].DisplayName; got != "Claude Opus 5" {
		t.Fatalf("new model display name = %q, want Claude Opus 5", got)
	}
	if got := entry.Changes.NewProviders[0].Name; got != "example-ai" {
		t.Fatalf("new provider name = %q, want example-ai", got)
	}
}

func TestRemoteCatalogPreservesLatestAdditionAndCanonicalIdentity(t *testing.T) {
	t.Parallel()

	additions, err := catalogAdditionsForVersion([]byte(`
versions:
  - version: "2.4.0"
    changes:
      new_models:
        - provider: aws-bedrock
          model: anthropic.claude-opus-5
`), "2.4.0")
	if err != nil {
		t.Fatalf("catalogAdditionsForVersion() error = %v", err)
	}

	model := convertModelToLegacy("aws-bedrock", map[string]interface{}{
		"name":         "anthropic.claude-opus-5",
		"display_name": "Claude Opus 5 (Bedrock)",
		"status":       "stable",
	}, additions["aws-bedrock/anthropic.claude-opus-5"])

	if got := model["added_in_version"]; got != "2.4.0" {
		t.Fatalf("added_in_version = %#v, want 2.4.0", got)
	}
	if got := model["publisher"]; got != "anthropic" {
		t.Fatalf("publisher = %#v, want anthropic", got)
	}
	if got := model["canonical_model_id"]; got != "anthropic/claude-opus-5" {
		t.Fatalf("canonical_model_id = %#v, want anthropic/claude-opus-5", got)
	}
}
