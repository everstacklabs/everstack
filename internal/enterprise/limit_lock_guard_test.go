package enterprise

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every create path must take the per-organization advisory lock
// (ReserveLimitSlot): an unlocked count-then-insert lets two racing creators
// at cap minus one both through (TestPGLastSlotIsSerializedForEveryCap shows
// the race). PrecheckLimit is the only unlocked form, for an early refusal
// before expensive work; the create itself must still reserve a slot.
func TestCreatePathsTakeThePerOrganizationLock(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	unlocked := regexp.MustCompile(`enterprise\.(CheckLimit|CheckResourceLimit|ReserveResourceSlot)\(`)
	var offenders []string
	for _, dir := range []string{"internal", "cmd", "services"} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if strings.Contains(path, string(filepath.Separator)+"enterprise"+string(filepath.Separator)) {
				return nil
			}
			body, err := os.ReadFile(path)
			if err == nil && unlocked.Match(body) {
				rel, _ := filepath.Rel(root, path)
				offenders = append(offenders, rel)
			}
			return nil
		})
	}
	if len(offenders) > 0 {
		t.Fatalf("these files run an unlocked plan cap check on a create path; use ReserveLimitSlot "+
			"(or PrecheckLimit for an early refusal followed by a reserved create): %v", offenders)
	}
}
