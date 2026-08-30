package firecracker

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/functions/isolation"
)

func TestRuntimeRootfsFallback(t *testing.T) {
	b := &Backend{config: DefaultConfig()}
	delete(b.config.RuntimeRootfs, isolation.RuntimeNodeJS20)

	if got := b.rootfsForRuntime(isolation.RuntimeNodeJS20); got != "base" {
		t.Fatalf("expected fallback rootfs 'base', got %q", got)
	}
}
