package billingidentity

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// organizations.plan_limit_exempt switches plan caps off for an organization,
// so a customer must never be able to set or influence it. It is settable
// only by migration or ops tooling. This guard walks the repository and
// fails if anything other than the read-only scope lookup and the migrations
// names the flag: a proto field, a customer RPC handler, a repository
// INSERT/UPDATE, a frontend form or an API client would all show up here.
func TestPlanLimitExemptIsUnreachableFromCustomerAPIs(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repository root not found at %s: %v", root, err)
	}

	flag := regexp.MustCompile(`(?i)plan_?limit_?exempt`)
	allowed := []string{
		"internal/billingidentity/limit_scope.go", // read-only SELECT
		"services/cloud/internal/database/migrations/postgres/organizations_plan_limit_exempt_",
	}
	skipDirs := map[string]bool{
		".git": true, "node_modules": true, "dist": true, ".artifacts": true,
		".turbo": true, ".worktrees": true,
	}
	scanned := map[string]bool{
		".go": true, ".proto": true, ".sql": true, ".ts": true, ".tsx": true,
		".yaml": true, ".yml": true, ".json": true,
	}

	var offenders []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !scanned[filepath.Ext(path)] || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		for _, prefix := range allowed {
			if strings.HasPrefix(rel, prefix) {
				return nil
			}
		}
		info, err := d.Info()
		if err != nil || info.Size() > 4<<20 {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if flag.Match(body) {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("plan_limit_exempt must be settable only by migration or ops tooling, "+
			"but these files name it: %v", offenders)
	}

	// The reader must stay a read: the lookup selects the flag, it never
	// writes it.
	reader, err := os.ReadFile(filepath.Join(root, "internal", "billingidentity", "limit_scope.go"))
	if err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile(`(?is)(update|insert)[^;` + "`" + `]*plan_limit_exempt`).Match(reader) {
		t.Fatal("the scope lookup must only read plan_limit_exempt")
	}
}
