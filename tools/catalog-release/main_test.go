package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/catalogdistribution"
)

func TestRunValidateChecksCatalogWithoutReleaseCredentials(t *testing.T) {
	var output bytes.Buffer
	wantVersion := readCatalogVersion(t, "../../model-catalog")

	if err := run(
		context.Background(),
		[]string{"validate", "--catalog-dir", "../../model-catalog"},
		func(string) string { return "" },
		time.Now,
		&output,
		nil,
	); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(output.String(), "catalog "+wantVersion+" validated") {
		t.Fatalf("run() output = %q", output.String())
	}
}

func TestRunValidateRejectsModelMissingFromManifest(t *testing.T) {
	catalogDir := filepath.Join(t.TempDir(), "catalog")
	if err := os.CopyFS(catalogDir, os.DirFS("../../model-catalog")); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(catalogDir, "providers", "openai", "models", "pipeline-test-model.yaml")
	if err := os.WriteFile(modelPath, []byte(`name: pipeline-test-model
display_name: Pipeline Test Model
family: test
status: stable
release_date: "2026-08-20"
cost:
  input_per_1k: 0.001
  output_per_1k: 0.002
limits:
  max_tokens: 1024
  max_completion_tokens: 512
capabilities: [chat]
modalities:
  input: [text]
  output: [text]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	err := run(
		context.Background(),
		[]string{"validate", "--catalog-dir", catalogDir},
		func(string) string { return "" },
		time.Now,
		io.Discard,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "manifest is stale") {
		t.Fatalf("run() error = %v, want stale manifest rejection", err)
	}
}

func TestRunValidateRejectsStaleManifestStatistics(t *testing.T) {
	catalogDir := filepath.Join(t.TempDir(), "catalog")
	if err := os.CopyFS(catalogDir, os.DirFS("../../model-catalog")); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(catalogDir, "manifest.yaml")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest = regexp.MustCompile(`(?m)^  total_models: [0-9]+$`).ReplaceAll(manifest, []byte("  total_models: 0"))
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatal(err)
	}

	err = run(
		context.Background(),
		[]string{"validate", "--catalog-dir", catalogDir},
		func(string) string { return "" },
		time.Now,
		io.Discard,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "manifest is stale: statistics") {
		t.Fatalf("run() error = %v, want stale manifest statistics rejection", err)
	}
}

func TestRunValidateRejectsMalformedModelOutsideLatestChangelog(t *testing.T) {
	catalogDir := filepath.Join(t.TempDir(), "catalog")
	if err := os.CopyFS(catalogDir, os.DirFS("../../model-catalog")); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(catalogDir, "providers", "perplexity", "models", "sonar.yaml")
	if err := os.WriteFile(modelPath, []byte("name: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := run(
		context.Background(),
		[]string{"validate", "--catalog-dir", catalogDir},
		func(string) string { return "" },
		time.Now,
		io.Discard,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "sonar.yaml") {
		t.Fatalf("run() error = %v, want malformed model rejection", err)
	}
}

func TestRunValidateRejectsUnknownLatestChangelogModel(t *testing.T) {
	catalogDir := filepath.Join(t.TempDir(), "catalog")
	if err := os.CopyFS(catalogDir, os.DirFS("../../model-catalog")); err != nil {
		t.Fatal(err)
	}
	changelogPath := filepath.Join(catalogDir, "changelog.yaml")
	changelog, err := os.ReadFile(changelogPath)
	if err != nil {
		t.Fatal(err)
	}
	// Rewrite the FIRST new_models entry in the file rather than a named
	// model. changelog.yaml is ordered newest-first, so the first entry always
	// belongs to the release version.txt names, which is the only one
	// validation inspects. Naming a specific model coupled this test to that
	// model still being part of the newest release, and it broke as soon as a
	// release shipped without it.
	entry := regexp.MustCompile(`(?m)^(\s*- \{ provider: )([A-Za-z0-9_.-]+)(, model: )"[^"]+"`)
	loc := entry.FindSubmatchIndex(changelog)
	if loc == nil {
		t.Fatal("changelog has no new_models entry to rewrite")
	}
	provider := string(changelog[loc[4]:loc[5]])
	rewritten := make([]byte, 0, len(changelog)+32)
	rewritten = append(rewritten, changelog[:loc[2]]...)
	rewritten = append(rewritten, changelog[loc[2]:loc[3]]...)
	rewritten = append(rewritten, provider...)
	rewritten = append(rewritten, changelog[loc[6]:loc[7]]...)
	rewritten = append(rewritten, []byte(`"missing-pipeline-model"`)...)
	rewritten = append(rewritten, changelog[loc[1]:]...)
	changelog = rewritten

	if err := os.WriteFile(changelogPath, changelog, 0o644); err != nil {
		t.Fatal(err)
	}
	wantMessage := fmt.Sprintf(
		`latest changelog model %q does not exist`,
		provider+"/missing-pipeline-model",
	)

	err = run(
		context.Background(),
		[]string{"validate", "--catalog-dir", catalogDir},
		func(string) string { return "" },
		time.Now,
		io.Discard,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), wantMessage) {
		t.Fatalf("run() error = %v, want %s", err, wantMessage)
	}
}

func TestRunBuildsVerifiableRelease(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()
	fixedTime := time.Date(2026, 8, 17, 16, 30, 0, 0, time.UTC)
	getenv := func(key string) string {
		if key == signingKeyEnvironmentVariable {
			return base64.StdEncoding.EncodeToString(privateKey)
		}
		return ""
	}

	if err := run(
		context.Background(),
		[]string{"build", "--catalog-dir", "../../model-catalog", "--out", outDir},
		getenv,
		func() time.Time { return fixedTime },
		os.Stdout,
		nil,
	); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	versionData, err := os.ReadFile("../../model-catalog/version.txt")
	if err != nil {
		t.Fatal(err)
	}
	wantVersion := string(bytes.TrimSpace(versionData))

	channelData, err := os.ReadFile(filepath.Join(outDir, "channels", "stable.json"))
	if err != nil {
		t.Fatal(err)
	}
	channel, err := catalogdistribution.VerifyChannel(
		map[string]ed25519.PublicKey{catalogdistribution.PublicKeyID(publicKey): publicKey},
		channelData,
		"stable",
	)
	if err != nil {
		t.Fatalf("VerifyChannel() error = %v", err)
	}
	if channel.Version != wantVersion || channel.PublishedAt != fixedTime.Format(time.RFC3339) {
		t.Fatalf("channel = %#v", channel)
	}
	if _, err := os.Stat(filepath.Join(outDir, filepath.FromSlash(channel.BundlePath))); err != nil {
		t.Fatal(err)
	}
}

func TestRunBuildsWithProtectedSigningKeyFile(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyPath := filepath.Join(t.TempDir(), "catalog-signing.key")
	if err := os.WriteFile(
		privateKeyPath,
		[]byte(base64.StdEncoding.EncodeToString(privateKey)),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	getenv := func(key string) string {
		if key == signingKeyFileEnvironmentVariable {
			return privateKeyPath
		}
		return ""
	}
	if err := run(
		context.Background(),
		[]string{"build", "--catalog-dir", "../../model-catalog", "--out", t.TempDir()},
		getenv,
		time.Now,
		io.Discard,
		nil,
	); err != nil {
		t.Fatalf("run() error = %v", err)
	}
}

func TestLoadPrivateKeyRejectsGroupReadableFile(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyPath := filepath.Join(t.TempDir(), "catalog-signing.key")
	if err := os.WriteFile(
		privateKeyPath,
		[]byte(base64.StdEncoding.EncodeToString(privateKey)),
		0o640,
	); err != nil {
		t.Fatal(err)
	}

	_, err = loadPrivateKey("", privateKeyPath)
	if err == nil || !strings.Contains(err.Error(), "group or others") {
		t.Fatalf("loadPrivateKey() error = %v, want unsafe permissions rejection", err)
	}
}

func TestRunRejectsLegacyPublishFlagThatSkipsPublicVerification(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	getenv := func(key string) string {
		if key == signingKeyEnvironmentVariable {
			return base64.StdEncoding.EncodeToString(privateKey)
		}
		return ""
	}

	err = run(
		context.Background(),
		[]string{"--publish", "--catalog-dir", "../../model-catalog"},
		getenv,
		time.Now,
		io.Discard,
		func(context.Context, func(string) string, releaseArtifacts) error {
			t.Fatal("legacy publish flag reached publisher")
			return nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("run() error = %v, want explicit-command rejection", err)
	}
}

func TestRunPublishVerifiesThePromotedReleaseThroughThePublicDomain(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var published releaseArtifacts
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/channels/stable.json":
			response.Header().Set("Cache-Control", channelCacheControl)
			response.Header().Set("ETag", `"channel"`)
			_, _ = response.Write(published.Channel)
		case "/v1/" + published.BundlePath:
			response.Header().Set("Cache-Control", bundleCacheControl)
			response.Header().Set("ETag", `"bundle"`)
			_, _ = response.Write(published.Bundle)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)

	getenv := func(key string) string {
		if key == signingKeyEnvironmentVariable {
			return base64.StdEncoding.EncodeToString(privateKey)
		}
		return ""
	}
	outDir := t.TempDir()
	publish := func(_ context.Context, _ func(string) string, artifacts releaseArtifacts) error {
		if len(artifacts.Bundle) == 0 || len(artifacts.Channel) == 0 {
			t.Fatal("publisher received incomplete artifacts")
		}
		if _, err := os.Stat(filepath.Join(outDir, filepath.FromSlash(artifacts.BundlePath))); err != nil {
			t.Fatalf("publisher called before local bundle was written: %v", err)
		}
		if _, err := os.Stat(filepath.Join(outDir, "channels", artifacts.ChannelName+".json")); err != nil {
			t.Fatalf("publisher called before local channel was written: %v", err)
		}
		published = artifacts
		return nil
	}
	var output bytes.Buffer
	wantVersion := readCatalogVersion(t, "../../model-catalog")

	if err := run(
		context.Background(),
		[]string{
			"publish",
			"--catalog-dir", "../../model-catalog",
			"--out", outDir,
			"--public-url", server.URL + "/v1",
			"--verify-timeout", "1s",
		},
		getenv,
		time.Now,
		&output,
		publish,
	); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if published.Version != wantVersion {
		t.Fatalf("published version = %q", published.Version)
	}
	if !strings.Contains(output.String(), "verified: "+server.URL+"/v1 channel stable version "+wantVersion) {
		t.Fatalf("run() output = %q", output.String())
	}
}

func readCatalogVersion(t *testing.T, catalogDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(catalogDir, "version.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func TestRunPublishRejectsPublicDistributionWithoutReleaseCacheHeaders(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var published releaseArtifacts
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/channels/stable.json":
			response.Header().Set("ETag", `"channel"`)
			_, _ = response.Write(published.Channel)
		case "/v1/" + published.BundlePath:
			response.Header().Set("ETag", `"bundle"`)
			_, _ = response.Write(published.Bundle)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)

	getenv := func(key string) string {
		if key == signingKeyEnvironmentVariable {
			return base64.StdEncoding.EncodeToString(privateKey)
		}
		return ""
	}
	publish := func(_ context.Context, _ func(string) string, artifacts releaseArtifacts) error {
		published = artifacts
		return nil
	}

	err = run(
		context.Background(),
		[]string{
			"publish",
			"--catalog-dir", "../../model-catalog",
			"--out", t.TempDir(),
			"--public-url", server.URL + "/v1",
			"--verify-timeout", "50ms",
		},
		getenv,
		time.Now,
		io.Discard,
		publish,
	)
	if err == nil || !strings.Contains(err.Error(), "Cache-Control") {
		t.Fatalf("run() error = %v, want public cache header rejection", err)
	}
}
