package provider_catalog

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"gopkg.in/yaml.v3"
)

func TestSortModelsByReleaseDateNewestFirst(t *testing.T) {
	t.Parallel()

	models := []ModelMetadata{
		{Name: "undated-z", DisplayName: "Zulu"},
		{Name: "older", DisplayName: "Older", ReleaseDate: "2026-05-28"},
		{Name: "newest", DisplayName: "Newest", ReleaseDate: "2026-07-24"},
		{Name: "undated-a", DisplayName: "Alpha"},
		{Name: "middle", DisplayName: "Middle", ReleaseDate: "2026-06-29"},
	}

	sortModelsByReleaseDate(models)

	got := make([]string, 0, len(models))
	for _, model := range models {
		got = append(got, model.Name)
	}
	want := []string{"newest", "middle", "older", "undated-a", "undated-z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("model order = %v, want %v", got, want)
	}
}

func TestCurrentCatalogExposesNewestOpenAIModelsFirst(t *testing.T) {
	t.Parallel()

	modelsYAML, providersYAML, err := validator.LoadCatalogFromDirectory("../../../model-catalog")
	if err != nil {
		t.Fatalf("load current catalog: %v", err)
	}
	models, err := validator.ParseModelsDefaults(modelsYAML)
	if err != nil {
		t.Fatalf("parse current models: %v", err)
	}
	var providers ProviderDefaultsConfig
	if err := validator.LoadYAMLIntoStruct(providersYAML, &providers); err != nil {
		t.Fatalf("parse current providers: %v", err)
	}

	service := &Service{
		catalog:   make(map[string]*ProviderCatalogEntry),
		models:    models,
		providers: &providers,
	}
	if err := service.buildCatalog(); err != nil {
		t.Fatalf("build current catalog: %v", err)
	}

	openAI, ok := service.GetProvider("openai")
	if !ok {
		t.Fatal("OpenAI provider not found")
	}
	if len(openAI.Models) < 4 {
		t.Fatalf("OpenAI model count = %d, want at least 4", len(openAI.Models))
	}
	got := make([]string, 0, 4)
	for _, model := range openAI.Models[:4] {
		got = append(got, model.Name)
	}
	want := []string{"gpt-5.6", "gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("newest OpenAI models = %v, want %v", got, want)
	}
}

// TestCurrentCatalogMarksCurrentReleaseAdditions drives the "new" marker off
// the changelog rather than a pinned version string. The loader only stamps
// added_in_version on models listed under the release that matches
// version.txt, so a model added two releases ago legitimately carries no
// marker and the assertion has to move with the catalog, not against it.
func TestCurrentCatalogMarksCurrentReleaseAdditions(t *testing.T) {
	t.Parallel()

	const catalogPath = "../../../model-catalog"

	version, err := validator.GetCatalogVersion(catalogPath)
	if err != nil {
		t.Fatalf("read catalog version: %v", err)
	}

	changelogData, err := os.ReadFile(filepath.Join(catalogPath, "changelog.yaml"))
	if err != nil {
		t.Fatalf("read catalog changelog: %v", err)
	}
	var changelog validator.CatalogChangelog
	if err := yaml.Unmarshal(changelogData, &changelog); err != nil {
		t.Fatalf("parse catalog changelog: %v", err)
	}

	var additions []validator.CatalogChangelogModel
	for _, entry := range changelog.Versions {
		if entry.Version == version {
			additions = entry.Changes.NewModels
			break
		}
	}
	if len(additions) == 0 {
		t.Fatalf("changelog has no new_models for current version %q", version)
	}

	modelsYAML, providersYAML, err := validator.LoadCatalogFromDirectory(catalogPath)
	if err != nil {
		t.Fatalf("load current catalog: %v", err)
	}
	models, err := validator.ParseModelsDefaults(modelsYAML)
	if err != nil {
		t.Fatalf("parse current models: %v", err)
	}
	var providers ProviderDefaultsConfig
	if err := validator.LoadYAMLIntoStruct(providersYAML, &providers); err != nil {
		t.Fatalf("parse current providers: %v", err)
	}

	service := &Service{
		catalog:   make(map[string]*ProviderCatalogEntry),
		models:    models,
		providers: &providers,
	}
	if err := service.buildCatalog(); err != nil {
		t.Fatalf("build current catalog: %v", err)
	}

	for _, addition := range additions {
		provider, ok := service.GetProvider(addition.Provider)
		if !ok {
			t.Errorf("changelog names provider %q, which the catalog does not expose", addition.Provider)
			continue
		}
		found := false
		for _, model := range provider.Models {
			if model.Name != addition.Model {
				continue
			}
			found = true
			if model.AddedInVersion != version {
				t.Errorf("%s/%s added version = %q, want %q",
					addition.Provider, addition.Model, model.AddedInVersion, version)
			}
			break
		}
		if !found {
			t.Errorf("changelog names %s/%s, which the catalog does not expose",
				addition.Provider, addition.Model)
		}
	}
}
