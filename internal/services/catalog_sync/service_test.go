package catalog_sync

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/commands"
)

type testCatalogDBReconciler struct {
	err          error
	errVersion   string
	calls        int
	versions     []string
	newModels    [][]string
	newProviders [][]string
}

func (r *testCatalogDBReconciler) ReconcileFromCatalog(_ context.Context, version, _ string, result *MergeResult) error {
	r.calls++
	r.versions = append(r.versions, version)
	r.newModels = append(r.newModels, append([]string(nil), result.NewModels...))
	r.newProviders = append(r.newProviders, append([]string(nil), result.NewProviders...))
	if r.errVersion != "" && version != r.errVersion {
		return nil
	}
	return r.err
}

type testCatalogRefresher struct {
	err   error
	calls int
}

func (r *testCatalogRefresher) Refresh() error {
	r.calls++
	return r.err
}

func TestAppliedCatalogSyncDoesNotRemainPending(t *testing.T) {
	t.Parallel()

	sourceDir := t.TempDir()
	writeTestCatalogFile(t, sourceDir, "version.txt", "2.4.0\n")
	writeTestCatalogFile(t, sourceDir, "models.yaml", `
providers:
  example:
    name: Example
    models:
      - name: example-model
        display_name: Example Model
`)
	writeTestCatalogFile(t, sourceDir, "providers.yaml", `
providers:
  example:
    display_name: Example
`)
	writeTestCatalogFile(t, sourceDir, "changelog.yaml", "versions: []\n")
	cacheDir := t.TempDir()

	config := &Config{
		Source:       "local",
		LocalPath:    sourceDir,
		CacheDir:     cacheDir,
		SyncInterval: time.Hour,
	}
	embeddedModels := &validator.ModelsConfig{Providers: map[string]validator.ProviderConfig{}}
	embeddedProviders := map[string]interface{}{"providers": map[string]interface{}{}}
	service := NewService(
		config,
		embeddedModels,
		embeddedProviders,
	)

	if err := service.syncCatalog(context.Background(), false); err != nil {
		t.Fatalf("syncCatalog() error = %v", err)
	}

	_, _, hasUpdates, newModels, newProviders, _ := service.GetStatus()
	if hasUpdates {
		t.Fatal("hasUpdates = true after the catalog was successfully applied")
	}
	if newModels != 1 || newProviders != 1 {
		t.Fatalf("change counts = (%d models, %d providers), want (1, 1)", newModels, newProviders)
	}

	if err := service.syncCatalog(context.Background(), true); err != nil {
		t.Fatalf("forced syncCatalog() error = %v", err)
	}
	_, _, _, newModels, newProviders, _ = service.GetStatus()
	if newModels != 1 || newProviders != 1 {
		t.Fatalf("same-version catalog change counts = (%d models, %d providers), want preserved (1, 1)", newModels, newProviders)
	}

	restored := NewService(
		config,
		&validator.ModelsConfig{Providers: map[string]validator.ProviderConfig{}},
		map[string]interface{}{"providers": map[string]interface{}{}},
	)
	providers := restored.GetCachedProviders()
	providerMap, ok := providers["providers"].(map[string]interface{})
	if !ok || providerMap["example"] == nil {
		t.Fatalf("restored providers = %#v, want cached example provider", providers)
	}
}

func TestStartDoesNotPutRemoteCatalogOnReadinessPath(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(response, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	t.Setenv("EVS_CATALOG_PUBLIC_KEY", "")
	t.Setenv("EVS_CATALOG_PUBLIC_KEYS", "")
	t.Setenv("EVS_CATALOG_REQUIRE_SIGNATURE", "false")

	service := NewService(&Config{
		Source:         "remote",
		RemoteURL:      server.URL,
		EnableAutoSync: true,
		SyncInterval:   time.Hour,
		CacheDir:       t.TempDir(),
		Timeout:        time.Second,
	}, &validator.ModelsConfig{Providers: map[string]validator.ProviderConfig{}}, map[string]interface{}{
		"providers": map[string]interface{}{},
	})
	ctx, cancel := context.WithCancel(context.Background())
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	time.Sleep(20 * time.Millisecond)
	if got := requests.Load(); got != 0 {
		t.Fatalf("remote catalog requests during service startup = %d, want 0", got)
	}
}

func TestCachedProjectionRecoveryRunsBeforeUnavailableSourceCheck(t *testing.T) {
	cacheDir := t.TempDir()
	cache := NewCache(cacheDir)
	if err := cache.SaveCatalog(&CatalogFiles{
		Version:   "2.0.0",
		Models:    cacheTestModels("cached-model"),
		Providers: cacheTestProviders(),
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(&Config{
		Source:       "local",
		LocalPath:    t.TempDir(),
		CacheDir:     cacheDir,
		SyncInterval: time.Hour,
	}, &validator.ModelsConfig{Providers: map[string]validator.ProviderConfig{}}, map[string]interface{}{
		"providers": map[string]interface{}{},
	})
	reconciler := &testCatalogDBReconciler{}
	service.SetDBReconciler(reconciler)

	err := service.syncCatalog(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "failed to fetch remote version") {
		t.Fatalf("syncCatalog() error = %v, want unavailable source", err)
	}
	if reconciler.calls != 1 || len(reconciler.versions) != 1 || reconciler.versions[0] != "2.0.0" {
		t.Fatalf("projection recovery calls/versions = (%d, %v), want cached 2.0.0 before source failure", reconciler.calls, reconciler.versions)
	}
}

func TestAutomaticSyncRejectsOlderCatalogVersion(t *testing.T) {
	sourceDir := t.TempDir()
	writeTestCatalogFile(t, sourceDir, "version.txt", "2.4.0\n")
	writeTestCatalogFile(t, sourceDir, "models.yaml", `
providers:
  example:
    name: Example
    models:
      - name: replayed-model
        display_name: Replayed Model
`)
	writeTestCatalogFile(t, sourceDir, "providers.yaml", `
providers:
  example:
    display_name: Example
`)
	writeTestCatalogFile(t, sourceDir, "changelog.yaml", "versions: []\n")
	cacheDir := t.TempDir()
	cache := NewCache(cacheDir)
	if err := cache.SaveCatalog(&CatalogFiles{
		Version: "2.5.0",
		Models: []byte(`
providers:
  example:
    name: Example
    models:
      - name: current-model
        display_name: Current Model
`),
		Providers: []byte(`
providers:
  example:
    display_name: Example
`),
		Changelog: []byte("versions: []\n"),
	}); err != nil {
		t.Fatal(err)
	}

	service := NewService(&Config{
		Source:       "local",
		LocalPath:    sourceDir,
		CacheDir:     cacheDir,
		SyncInterval: time.Hour,
	}, &validator.ModelsConfig{Providers: map[string]validator.ProviderConfig{}}, map[string]interface{}{
		"providers": map[string]interface{}{},
	})
	if err := service.syncCatalog(context.Background(), false); err != nil {
		t.Fatalf("syncCatalog() error = %v", err)
	}

	version, _, _, _, _, _ := service.GetStatus()
	if version != "2.5.0" {
		t.Fatalf("current version = %q, want 2.5.0", version)
	}
	files, err := cache.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if files.Version != "2.5.0" || !strings.Contains(string(files.Models), "current-model") {
		t.Fatalf("cached catalog was downgraded: %#v", files)
	}
}

func TestSyncActivatesCatalogBeforeRetryableDatabaseProjection(t *testing.T) {
	sourceDir := t.TempDir()
	writeTestCatalogFile(t, sourceDir, "version.txt", "2.0.0\n")
	writeTestCatalogFile(t, sourceDir, "models.yaml", `
providers:
  candidate:
    name: Candidate
    models:
      - name: candidate-model
        display_name: Candidate Model
`)
	writeTestCatalogFile(t, sourceDir, "providers.yaml", `
providers:
  candidate:
    display_name: Candidate
`)
	writeTestCatalogFile(t, sourceDir, "changelog.yaml", "versions: []\n")
	cacheDir := t.TempDir()
	cache := NewCache(cacheDir)
	if err := cache.SaveCatalog(&CatalogFiles{
		Version:   "1.0.0",
		Models:    cacheTestModels("active-model"),
		Providers: cacheTestProviders(),
	}); err != nil {
		t.Fatal(err)
	}

	service := NewService(&Config{
		Source:       "local",
		LocalPath:    sourceDir,
		CacheDir:     cacheDir,
		SyncInterval: time.Hour,
	}, &validator.ModelsConfig{Providers: map[string]validator.ProviderConfig{}}, map[string]interface{}{
		"providers": map[string]interface{}{},
	})
	reconciler := &testCatalogDBReconciler{err: errors.New("database unavailable"), errVersion: "2.0.0"}
	refresher := &testCatalogRefresher{}
	service.SetDBReconciler(reconciler)
	service.SetCatalogRefresher(refresher)

	err := service.syncCatalog(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "database unavailable") {
		t.Fatalf("syncCatalog() error = %v, want reconciliation failure", err)
	}
	if reconciler.calls != 2 || refresher.calls != 1 {
		t.Fatalf("calls = (%d reconciler, %d refresher), want old and new projections plus one refresh", reconciler.calls, refresher.calls)
	}
	assertCachedCatalogVersion(t, cache, "2.0.0", "candidate-model")
	activeFiles, loadErr := cache.LoadCatalog()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(activeFiles.ProjectionNewModels) == 0 || len(activeFiles.ProjectionNewProviders) == 0 {
		t.Fatalf("atomic projection delta = (%v, %v), want new model and provider",
			activeFiles.ProjectionNewModels, activeFiles.ProjectionNewProviders)
	}
	version, _, _, _, _, _ := service.GetStatus()
	if version != "2.0.0" {
		t.Fatalf("current version = %q, want 2.0.0", version)
	}

	reconciler.err = nil
	if err := service.syncCatalog(context.Background(), true); err != nil {
		t.Fatalf("projection retry error = %v", err)
	}
	if reconciler.calls != 3 || refresher.calls != 1 {
		t.Fatalf("retry calls = (%d reconciler, %d refresher), want (3, 1)", reconciler.calls, refresher.calls)
	}
	if len(reconciler.newModels[1]) == 0 || len(reconciler.newProviders[1]) == 0 {
		t.Fatalf("initial projection delta = (%v, %v), want new model and provider", reconciler.newModels[1], reconciler.newProviders[1])
	}
	if strings.Join(reconciler.newModels[2], ",") != strings.Join(reconciler.newModels[1], ",") ||
		strings.Join(reconciler.newProviders[2], ",") != strings.Join(reconciler.newProviders[1], ",") {
		t.Fatalf("retried projection delta = (%v, %v), want (%v, %v)",
			reconciler.newModels[2], reconciler.newProviders[2], reconciler.newModels[1], reconciler.newProviders[1])
	}
	metadata, err := cache.GetMetadata()
	if err != nil {
		t.Fatal(err)
	}
	if metadata.ProjectionVersion != "2.0.0" {
		t.Fatalf("projection version = %q, want 2.0.0", metadata.ProjectionVersion)
	}
}

func TestPersistedEventPublicationFailureKeepsLocalProjectionPending(t *testing.T) {
	cacheDir := t.TempDir()
	service := NewService(&Config{
		Source:       "local",
		LocalPath:    t.TempDir(),
		CacheDir:     cacheDir,
		SyncInterval: time.Hour,
	}, &validator.ModelsConfig{Providers: map[string]validator.ProviderConfig{}}, map[string]interface{}{
		"providers": map[string]interface{}{},
	})
	reconciler := &testCatalogDBReconciler{err: &commands.PostCommitError{Err: errors.New("subscriber unavailable")}}
	service.SetDBReconciler(reconciler)

	files := &CatalogFiles{
		Version:   "2.0.0",
		Models:    cacheTestModels("example-model"),
		Providers: cacheTestProviders(),
	}
	err := service.reconcileProjection(context.Background(), files, &MergeResult{}, &CacheMetadata{Version: "2.0.0"})
	if err == nil || !commands.EventWasPersisted(err) {
		t.Fatalf("reconcileProjection() error = %v, want persisted post-commit error", err)
	}
	metadata, loadErr := service.cache.GetMetadata()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if metadata.ProjectionVersion != "" {
		t.Fatalf("projection version = %q, want pending marker", metadata.ProjectionVersion)
	}
	if reconciler.calls != 1 {
		t.Fatalf("reconciler calls = %d, want 1", reconciler.calls)
	}
}

func TestLocalProjectionCheckpointFailureDoesNotBlockJournalCompletion(t *testing.T) {
	cacheDir := t.TempDir()
	service := NewService(&Config{
		Source:       "local",
		LocalPath:    t.TempDir(),
		CacheDir:     cacheDir,
		SyncInterval: time.Hour,
	}, &validator.ModelsConfig{Providers: map[string]validator.ProviderConfig{}}, map[string]interface{}{
		"providers": map[string]interface{}{},
	})
	reconciler := &testCatalogDBReconciler{}
	service.SetDBReconciler(reconciler)
	files := &CatalogFiles{
		Version:   "2.0.0",
		Models:    cacheTestModels("example-model"),
		Providers: cacheTestProviders(),
	}
	metadataPath := filepath.Join(cacheDir, "metadata.json")
	if err := os.Mkdir(metadataPath, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := service.reconcileProjection(context.Background(), files, &MergeResult{}, &CacheMetadata{Version: "2.0.0"}); err != nil {
		t.Fatalf("reconcileProjection() error = %v, want journal completion despite local checkpoint failure", err)
	}
	if err := os.Remove(metadataPath); err != nil {
		t.Fatal(err)
	}
	if err := service.reconcileProjection(context.Background(), files, &MergeResult{}, &CacheMetadata{Version: "2.0.0"}); err != nil {
		t.Fatalf("checkpoint retry: %v", err)
	}
	metadata, err := service.cache.GetMetadata()
	if err != nil {
		t.Fatal(err)
	}
	if metadata.ProjectionVersion != "2.0.0" {
		t.Fatalf("projection version = %q, want 2.0.0", metadata.ProjectionVersion)
	}
}

func TestSyncRetriesSameReleaseAfterProviderRefreshFails(t *testing.T) {
	sourceDir := t.TempDir()
	writeTestCatalogFile(t, sourceDir, "version.txt", "2.0.0\n")
	writeTestCatalogFile(t, sourceDir, "models.yaml", string(cacheTestModels("candidate-model")))
	writeTestCatalogFile(t, sourceDir, "providers.yaml", string(cacheTestProviders()))
	writeTestCatalogFile(t, sourceDir, "changelog.yaml", "versions: []\n")
	cacheDir := t.TempDir()
	cache := NewCache(cacheDir)
	if err := cache.SaveCatalog(&CatalogFiles{
		Version:   "1.0.0",
		Models:    cacheTestModels("active-model"),
		Providers: cacheTestProviders(),
	}); err != nil {
		t.Fatal(err)
	}

	service := NewService(&Config{
		Source:       "local",
		LocalPath:    sourceDir,
		CacheDir:     cacheDir,
		SyncInterval: time.Hour,
	}, &validator.ModelsConfig{Providers: map[string]validator.ProviderConfig{}}, map[string]interface{}{
		"providers": map[string]interface{}{},
	})
	refresher := &testCatalogRefresher{err: errors.New("refresh unavailable")}
	service.SetCatalogRefresher(refresher)

	err := service.syncCatalog(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "refresh unavailable") {
		t.Fatalf("syncCatalog() error = %v, want refresh failure", err)
	}
	assertCachedCatalogVersion(t, cache, "1.0.0", "active-model")
	version, _, _, _, _, _ := service.GetStatus()
	if version != "1.0.0" {
		t.Fatalf("current version = %q, want 1.0.0", version)
	}

	refresher.err = nil
	if err := service.syncCatalog(context.Background(), false); err != nil {
		t.Fatalf("retry syncCatalog() error = %v", err)
	}
	assertCachedCatalogVersion(t, cache, "2.0.0", "candidate-model")
	version, _, _, _, _, _ = service.GetStatus()
	if version != "2.0.0" {
		t.Fatalf("current version after retry = %q, want 2.0.0", version)
	}
	if refresher.calls != 2 {
		t.Fatalf("refresher calls = %d, want 2", refresher.calls)
	}
}

func TestForcedSyncRejectsOlderCatalogVersion(t *testing.T) {
	sourceDir := t.TempDir()
	writeTestCatalogFile(t, sourceDir, "version.txt", "2.4.0\n")
	writeTestCatalogFile(t, sourceDir, "models.yaml", `
providers:
  example:
    name: Example
    models:
      - name: replayed-model
        display_name: Replayed Model
`)
	writeTestCatalogFile(t, sourceDir, "providers.yaml", `
providers:
  example:
    display_name: Example
`)
	writeTestCatalogFile(t, sourceDir, "changelog.yaml", "versions: []\n")
	cacheDir := t.TempDir()
	cache := NewCache(cacheDir)
	if err := cache.SaveCatalog(&CatalogFiles{
		Version: "2.5.0",
		Models: []byte(`
providers:
  example:
    name: Example
    models:
      - name: current-model
        display_name: Current Model
`),
		Providers: []byte(`
providers:
  example:
    display_name: Example
`),
	}); err != nil {
		t.Fatal(err)
	}

	service := NewService(&Config{
		Source:       "local",
		LocalPath:    sourceDir,
		CacheDir:     cacheDir,
		SyncInterval: time.Hour,
	}, &validator.ModelsConfig{Providers: map[string]validator.ProviderConfig{}}, map[string]interface{}{
		"providers": map[string]interface{}{},
	})
	err := service.syncCatalog(context.Background(), true)
	if err == nil || !strings.Contains(err.Error(), "refusing to apply older") {
		t.Fatalf("forced syncCatalog() error = %v, want downgrade rejection", err)
	}
	files, loadErr := cache.LoadCatalog()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if files.Version != "2.5.0" || !strings.Contains(string(files.Models), "current-model") {
		t.Fatalf("forced sync downgraded cached catalog: %#v", files)
	}
}

func writeTestCatalogFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func assertCachedCatalogVersion(t *testing.T, cache *Cache, version, model string) {
	t.Helper()
	files, err := cache.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if files.Version != version || !strings.Contains(string(files.Models), model) {
		t.Fatalf("cached catalog = %#v, want version %s with model %s", files, version, model)
	}
}
