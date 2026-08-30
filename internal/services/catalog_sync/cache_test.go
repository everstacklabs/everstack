package catalog_sync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCatalogIgnoresInterruptedLegacyFileUpdate(t *testing.T) {
	cacheDir := t.TempDir()
	cache := NewCache(cacheDir)
	original := &CatalogFiles{
		Version:   "1.0.0",
		Models:    cacheTestModels("model-v1"),
		Providers: cacheTestProviders(),
		Changelog: []byte("changelog-v1"),
	}
	if err := cache.SaveCatalog(original); err != nil {
		t.Fatalf("SaveCatalog() error = %v", err)
	}

	// Simulate a process stopping halfway through the old multi-file save
	// sequence for the next release.
	if err := os.WriteFile(filepath.Join(cacheDir, "models.yaml"), []byte("models-v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "version.txt"), []byte("2.0.0"), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := cache.LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog() error = %v", err)
	}
	if loaded.Version != original.Version || string(loaded.Models) != string(original.Models) || string(loaded.Providers) != string(original.Providers) {
		t.Fatalf("LoadCatalog() = %#v, want intact release %#v", loaded, original)
	}
}

func TestSaveCatalogLeavesLegacyMigrationFilesUntouched(t *testing.T) {
	cacheDir := t.TempDir()
	legacyFiles := map[string]string{
		"models.yaml":    string(cacheTestModels("legacy-model")),
		"providers.yaml": string(cacheTestProviders()),
		"version.txt":    "1.0.0",
		"changelog.yaml": "legacy-changelog",
	}
	for name, contents := range legacyFiles {
		if err := os.WriteFile(filepath.Join(cacheDir, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cache := NewCache(cacheDir)
	if err := cache.SaveCatalog(&CatalogFiles{
		Version:   "2.0.0",
		Models:    cacheTestModels("current-model"),
		Providers: cacheTestProviders(),
		Changelog: []byte("current-changelog"),
	}); err != nil {
		t.Fatalf("SaveCatalog() error = %v", err)
	}

	loaded, err := cache.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != "2.0.0" || string(loaded.Models) != string(cacheTestModels("current-model")) {
		t.Fatalf("LoadCatalog() = %#v, want current atomic bundle", loaded)
	}
	for name, want := range legacyFiles {
		got, err := os.ReadFile(filepath.Join(cacheDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("legacy file %s = %q, want untouched %q", name, got, want)
		}
	}
}

func TestGetCachedVersionRejectsCorruptAtomicBundle(t *testing.T) {
	cacheDir := t.TempDir()
	cache := NewCache(cacheDir)
	if err := cache.SaveCatalog(&CatalogFiles{
		Version:   "1.0.0",
		Models:    cacheTestModels("model-v1"),
		Providers: cacheTestProviders(),
	}); err != nil {
		t.Fatalf("SaveCatalog() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, catalogBundleFilename), []byte("not-json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "version.txt"), []byte("9.9.9"), 0o644); err != nil {
		t.Fatal(err)
	}

	if version, err := cache.GetCachedVersion(); err == nil {
		t.Fatalf("GetCachedVersion() = %q, want corrupt bundle error", version)
	}
}

func cacheTestModels(modelName string) []byte {
	return []byte("providers:\n  example:\n    models:\n      - name: " + modelName + "\n")
}

func cacheTestProviders() []byte {
	return []byte("providers:\n  example:\n    display_name: Example\n")
}
