package branding

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// everstack.dev is not ours to link to. DNS resolves it but nothing serves
// HTTPS, so every reference is a dead link in front of a customer, and the
// registration is outside our control.
//
// Five had accumulated in shipped code before this guard existed, including
// two "upgrade at" pricing links inside quota errors and the API-key error
// returned to every unauthenticated gateway request. They are easy to add by
// habit and invisible until someone clicks one, so this fails the build
// instead of waiting for a report.
var deadDomain = regexp.MustCompile(`everstack\.dev(?:[/"'` + "`" + `)\s]|$)`)

func TestNoReferencesToTheDeadDotDevDomain(t *testing.T) {
	root := repoRoot(t)

	var offenders []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // unreadable entries are not this test's business
		}
		if info.IsDir() {
			switch info.Name() {
			// Vendored and generated trees, plus the historical rebrand notes
			// which legitimately discuss the domain as a rejected option.
			case "node_modules", ".git", "dist", "vendor", ".worktrees", "enhancements":
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		// .mdx and .md matter as much as code and were missed originally: docs
		// are the likeliest place a stale link reappears, a reader clicks docs
		// links rather than reading source, and everstack.dev is still
		// unregistered, so a link there is a live hijack vector rather than a
		// cosmetic dead end. The guard had the right check and the wrong
		// locations.
		case ".go", ".ts", ".tsx", ".mdx", ".md":
		default:
			return nil
		}
		if strings.HasSuffix(path, "dead_domain_test.go") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		for i, line := range strings.Split(string(content), "\n") {
			if deadDomain.MatchString(line) {
				rel, _ := filepath.Rel(root, path)
				offenders = append(offenders, rel+":"+itoa(i+1))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repo: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("everstack.dev is a dead domain we do not control; use everstack.ai:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// repoRoot walks up until it finds go.mod, so the test works regardless of
// where `go test` is invoked from.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repo root (no go.mod found walking up)")
		}
		dir = parent
	}
}
