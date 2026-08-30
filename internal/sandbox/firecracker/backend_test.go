package firecracker

import (
	"context"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

func TestListReturnsEmptySliceWhenNoVMs(t *testing.T) {
	b := &FirecrackerBackend{
		vms: make(map[string]*MicroVM),
	}

	instances, err := b.List(context.Background())
	if err != nil {
		t.Fatalf("List() returned error: %v", err)
	}
	if instances == nil {
		t.Fatalf("List() returned nil slice, expected empty slice")
	}
	if len(instances) != 0 {
		t.Fatalf("List() returned %d instances, expected 0", len(instances))
	}
}

func TestListReturnsRunningVMs(t *testing.T) {
	created := time.Now().UTC()
	expires := created.Add(10 * time.Minute)

	b := &FirecrackerBackend{
		vms: map[string]*MicroVM{
			"sbx-b": {
				ID:        "vm-b",
				Status:    sandbox.StatusRunning,
				Config:    sandbox.InstanceConfig{SessionID: "sess-b", TenantID: "tenant-b"},
				CreatedAt: created,
				ExpiresAt: expires,
			},
			"sbx-a": {
				ID:        "vm-a",
				Status:    sandbox.StatusRunning,
				Config:    sandbox.InstanceConfig{SessionID: "sess-a", TenantID: "tenant-a"},
				CreatedAt: created,
				ExpiresAt: expires,
			},
		},
	}

	instances, err := b.List(context.Background())
	if err != nil {
		t.Fatalf("List() returned error: %v", err)
	}
	if len(instances) != 2 {
		t.Fatalf("List() returned %d instances, expected 2", len(instances))
	}

	// List() sorts by sandbox ID for stable restore behavior.
	if instances[0].ID != "sbx-a" || instances[1].ID != "sbx-b" {
		t.Fatalf("List() returned unexpected ordering: %q, %q", instances[0].ID, instances[1].ID)
	}

	if instances[0].Config.SessionID != "sess-a" || instances[1].Config.SessionID != "sess-b" {
		t.Fatalf("List() lost instance config data: %+v", instances)
	}
}
