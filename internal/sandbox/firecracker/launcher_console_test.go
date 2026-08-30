package firecracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadConsoleTail(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing file", func(t *testing.T) {
		got := readConsoleTail(filepath.Join(dir, "nope.log"), 1024)
		if !strings.HasPrefix(got, "<unavailable") {
			t.Fatalf("want <unavailable...>, got %q", got)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		p := filepath.Join(dir, "empty.log")
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if got := readConsoleTail(p, 1024); got != "<empty>" {
			t.Fatalf("want <empty>, got %q", got)
		}
	})

	t.Run("whitespace only", func(t *testing.T) {
		p := filepath.Join(dir, "ws.log")
		if err := os.WriteFile(p, []byte("\n\n   \n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := readConsoleTail(p, 1024); got != "<empty>" {
			t.Fatalf("want <empty>, got %q", got)
		}
	})

	t.Run("short file returned whole", func(t *testing.T) {
		p := filepath.Join(dir, "short.log")
		if err := os.WriteFile(p, []byte("Kernel panic - not syncing"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := readConsoleTail(p, 1024); got != "Kernel panic - not syncing" {
			t.Fatalf("unexpected: %q", got)
		}
	})

	t.Run("long file truncated to tail", func(t *testing.T) {
		p := filepath.Join(dir, "long.log")
		// 5000 'a' bytes then a distinctive panic marker at the very end.
		body := strings.Repeat("a", 5000) + "OOM-KILLER-MARKER"
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		got := readConsoleTail(p, 64)
		if int64(len(got)) > 64 {
			t.Fatalf("tail longer than cap: %d bytes", len(got))
		}
		// The end of the file (the part that matters for a crash) must
		// survive truncation; the head must be dropped.
		if !strings.HasSuffix(got, "OOM-KILLER-MARKER") {
			t.Fatalf("tail dropped the end-of-file marker: %q", got)
		}
	})
}
