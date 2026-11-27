//go:build linux

package firecracker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenFileIDsSeesAnOpenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "held.ext4")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta, err := statFile(path)
	if err != nil {
		t.Fatal(err)
	}

	open, err := openFileIDs()
	if err != nil {
		t.Fatalf("openFileIDs: %v", err)
	}
	if _, ok := open[meta.id]; ok {
		t.Fatal("file reported open before anything opened it")
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	open, err = openFileIDs()
	if err != nil {
		t.Fatalf("openFileIDs: %v", err)
	}
	if _, ok := open[meta.id]; !ok {
		t.Fatal("open file not found by the /proc scan")
	}
}
