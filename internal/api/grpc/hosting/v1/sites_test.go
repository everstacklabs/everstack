package v1

import (
	"strings"
	"testing"

	hostingpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/hosting/v1"
)

func TestNormalizeSitePath(t *testing.T) {
	good := map[string]string{
		"index.html":      "index.html",
		"/index.html":     "index.html",
		"assets/app.js":   "assets/app.js",
		"/deep/a/b/c.css": "deep/a/b/c.css",
		"  spaced.html ":  "spaced.html",
		"UPPER/Case.HTML": "UPPER/Case.HTML",
	}
	for in, want := range good {
		got, err := normalizeSitePath(in)
		if err != nil {
			t.Errorf("normalizeSitePath(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("normalizeSitePath(%q) = %q, want %q", in, got, want)
		}
	}

	bad := []string{
		"",
		"/",
		"..",
		"../etc/passwd",
		"a/../../b",
		"a//b.html",
		`a\b.html`,
		"./a.html",
		"a/./b.html",
		strings.Repeat("x", 1030),
	}
	for _, in := range bad {
		if got, err := normalizeSitePath(in); err == nil {
			t.Errorf("normalizeSitePath(%q) = %q, expected error", in, got)
		}
	}
}

func TestValidateManifestLimits(t *testing.T) {
	entry := func(path string, size int64) *hostingpb.FileManifestEntry {
		return &hostingpb.FileManifestEntry{Path: path, SizeBytes: size}
	}

	if _, _, err := validateManifest(nil); err == nil {
		t.Error("empty manifest should fail")
	}

	if _, _, err := validateManifest([]*hostingpb.FileManifestEntry{
		entry("a.html", 10), entry("a.html", 10),
	}); err == nil {
		t.Error("duplicate paths should fail")
	}

	if _, _, err := validateManifest([]*hostingpb.FileManifestEntry{
		entry("big.bin", maxFileBytes+1),
	}); err == nil {
		t.Error("oversize file should fail")
	}

	// Three files that individually pass but exceed the total cap.
	if _, _, err := validateManifest([]*hostingpb.FileManifestEntry{
		entry("a.bin", maxFileBytes), entry("b.bin", maxFileBytes), entry("c.bin", maxFileBytes),
	}); err == nil {
		t.Error("total size over cap should fail")
	}

	files, total, err := validateManifest([]*hostingpb.FileManifestEntry{
		entry("index.html", 100),
		{Path: "data.csv", SizeBytes: 50, ContentType: "text/csv"},
	})
	if err != nil {
		t.Fatalf("valid manifest failed: %v", err)
	}
	if total != 150 || len(files) != 2 {
		t.Errorf("total=%d len=%d", total, len(files))
	}
	if files[0].contentType == "" || files[1].contentType != "text/csv" {
		t.Errorf("content types: %+v", files)
	}
}

func TestContentTypeFor(t *testing.T) {
	if ct := contentTypeFor("app.js", ""); !strings.Contains(ct, "javascript") {
		t.Errorf("js content type = %q", ct)
	}
	if ct := contentTypeFor("x.unknownext", ""); ct != "application/octet-stream" {
		t.Errorf("fallback = %q", ct)
	}
	if ct := contentTypeFor("x.html", "text/plain"); ct != "text/plain" {
		t.Errorf("explicit content type not honored: %q", ct)
	}
}

func TestBuildManifestNoindexAndCacheControl(t *testing.T) {
	s := &Server{cfg: Config{BaseDomain: "evs.run"}}
	site := &siteRow{
		ID: "site-1", Slug: "demo", Status: "active", Access: "public", SPAFallback: true,
		ModerationGeneration: 2,
	}
	files := []siteFileRow{
		{Path: "index.html", R2Key: "sites/demo/v1/index.html", ContentType: "text/html", SizeBytes: 10},
		{Path: "assets/app.js", R2Key: "sites/demo/v1/assets/app.js", ContentType: "text/javascript", SizeBytes: 20},
	}

	anon := s.buildManifest(site, 1, files, true, nil)
	if !anon.NoIndex {
		t.Error("anonymous manifest must be noindex")
	}
	if !anon.SPAFallback {
		t.Error("spa fallback lost")
	}
	if anon.SiteID != "site-1" || anon.ModerationGeneration != 2 || anon.Status != "active" {
		t.Fatalf("moderation identity lost: %+v", anon)
	}
	if anon.Files["/index.html"].CacheControl != "public, max-age=60" {
		t.Errorf("html cache control = %q", anon.Files["/index.html"].CacheControl)
	}
	if anon.Files["/assets/app.js"].CacheControl != "public, max-age=3600" {
		t.Errorf("asset cache control = %q", anon.Files["/assets/app.js"].CacheControl)
	}

	owned := s.buildManifest(site, 1, files, false, nil)
	if owned.NoIndex {
		t.Error("owned public site must not be noindex")
	}

	site.Access = "noindex"
	ownedNoindex := s.buildManifest(site, 1, files, false, nil)
	if !ownedNoindex.NoIndex {
		t.Error("access=noindex must set noindex")
	}

	site.Status = "disabled"
	disabled := s.buildManifest(site, 1, files, false, nil)
	if disabled.Status != "disabled" {
		t.Fatalf("disabled status = %q", disabled.Status)
	}
}
