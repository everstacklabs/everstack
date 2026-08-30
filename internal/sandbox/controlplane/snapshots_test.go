package controlplane

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/snapshots"
)

func TestSnapshotTenantIDUsesTenantThenOrganization(t *testing.T) {
	t.Parallel()

	tenantID, err := snapshotTenantID(sandbox.TenantInstanceScope{OrganizationID: "org-a", TenantID: "workspace-a", InstanceID: "inst-a"})
	if err != nil || tenantID != "workspace-a" {
		t.Fatalf("expected workspace tenant, got %q err=%v", tenantID, err)
	}
	tenantID, err = snapshotTenantID(sandbox.TenantInstanceScope{OrganizationID: "org-a"})
	if err != nil || tenantID != "org-a" {
		t.Fatalf("expected org fallback, got %q err=%v", tenantID, err)
	}
	if _, err := snapshotTenantID(sandbox.TenantInstanceScope{}); !errors.Is(err, ErrSnapshotScopeRequired) {
		t.Fatalf("expected scope error, got %v", err)
	}
}

func TestSnapshotServiceCreateValidatesAndScopesTenant(t *testing.T) {
	t.Parallel()

	repo := &fakeSnapshotRepo{}
	svc := NewSnapshotService(repo)
	ctx := context.Background()
	scope := sandbox.TenantInstanceScope{OrganizationID: "org-a", TenantID: "workspace-a", InstanceID: "inst-a"}

	if _, err := svc.CreateSnapshot(ctx, CreateSnapshotRequest{Scope: scope, Image: "alpine"}); !errors.Is(err, ErrSnapshotNameRequired) {
		t.Fatalf("expected name required, got %v", err)
	}
	if _, err := svc.CreateSnapshot(ctx, CreateSnapshotRequest{Scope: scope, Name: "base"}); !errors.Is(err, ErrSnapshotImageRequired) {
		t.Fatalf("expected image required, got %v", err)
	}
	snap, err := svc.CreateSnapshot(ctx, CreateSnapshotRequest{Scope: scope, Name: " base ", Image: "alpine"})
	if err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	if snap.TenantID != "workspace-a" || snap.Name != "base" || repo.created.TenantID != "workspace-a" {
		t.Fatalf("unexpected snapshot/create params: snap=%+v params=%+v", snap, repo.created)
	}
}

type fakeSnapshotRepo struct {
	created snapshots.CreateParams
}

func (r *fakeSnapshotRepo) Create(_ context.Context, p snapshots.CreateParams) (*snapshots.Snapshot, error) {
	r.created = p
	return &snapshots.Snapshot{
		ID:        "snap-a",
		TenantID:  p.TenantID,
		Name:      p.Name,
		State:     snapshots.StateActive,
		BaseImage: p.Image,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

func (r *fakeSnapshotRepo) GetByID(_ context.Context, tenantID, id string) (*snapshots.Snapshot, error) {
	return &snapshots.Snapshot{ID: id, TenantID: tenantID}, nil
}

func (r *fakeSnapshotRepo) ListByTenant(_ context.Context, tenantID string) ([]snapshots.Snapshot, error) {
	return []snapshots.Snapshot{{ID: "snap-a", TenantID: tenantID}}, nil
}

func (r *fakeSnapshotRepo) Delete(_ context.Context, _, _ string) error {
	return nil
}
