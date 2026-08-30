package httpcors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot walks up from the test's working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate the module root")
		}
		dir = parent
	}
}

// Three services each grew their own corsMiddleware, and all three were unsafe
// in the same two ways. This guards the whole tree against either pattern
// coming back, rather than trusting each new handler to get it right.
//
// The two failure modes:
//
//	Access-Control-Allow-Origin: <reflected Origin header>  + credentials
//	    any site can read authenticated responses using the victim's cookies
//
//	Access-Control-Allow-Origin: *  + credentials
//	    browsers reject the pair, so the credentialed call silently fails
func TestNoUnsafeCORSInTree(t *testing.T) {
	root := repoRoot(t)

	// Directories that legitimately set CORS headers under their own
	// allow-list logic, plus generated and vendored code.
	skipDirs := map[string]bool{
		"node_modules": true, "vendor": true, ".git": true,
		"dist": true, "build": true, "apps": true, "packages": true,
	}

	// Files with a reviewed, allow-list-based implementation.
	allowedFiles := map[string]bool{
		filepath.Join("internal", "api", "http", "middleware", "cors_runtime.go"): true,
		filepath.Join("internal", "lib", "httpcors", "httpcors.go"):               true,
		filepath.Join("internal", "sandbox", "proxy.go"):                          true,
	}

	var offenders []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil || allowedFiles[rel] {
			return nil
		}
		content, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		src := string(content)
		if !strings.Contains(src, "Access-Control-Allow-Origin") {
			return nil
		}

		// Reflecting the request's Origin header straight back.
		if strings.Contains(src, `"Access-Control-Allow-Origin", r.Header.Get("Origin")`) {
			offenders = append(offenders, rel+": reflects the raw Origin header")
		}
		// Wildcard together with credentials in the same file.
		if strings.Contains(src, `"Access-Control-Allow-Origin", "*"`) &&
			strings.Contains(src, `"Access-Control-Allow-Credentials", "true"`) {
			offenders = append(offenders, rel+`: emits "*" alongside credentials`)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(offenders) > 0 {
		t.Fatalf("unsafe CORS found. Route these through internal/lib/httpcors:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}
