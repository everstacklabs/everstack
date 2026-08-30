package controlplane

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

func TestVolumeTenantIDUsesTenantThenOrganization(t *testing.T) {
	t.Parallel()

	tenantID, err := volumeTenantID(sandbox.TenantInstanceScope{OrganizationID: "org-a", TenantID: "workspace-a", InstanceID: "inst-a"})
	if err != nil || tenantID != "workspace-a" {
		t.Fatalf("expected workspace tenant, got %q err=%v", tenantID, err)
	}
	tenantID, err = volumeTenantID(sandbox.TenantInstanceScope{OrganizationID: "org-a"})
	if err != nil || tenantID != "org-a" {
		t.Fatalf("expected org fallback, got %q err=%v", tenantID, err)
	}
	if _, err := volumeTenantID(sandbox.TenantInstanceScope{}); !errors.Is(err, ErrVolumeScopeRequired) {
		t.Fatalf("expected scope error, got %v", err)
	}
}

func TestVolumeServiceCreateValidatesAndScopesTenant(t *testing.T) {
	t.Parallel()

	repo := &fakeVolumeRepo{}
	svc := NewVolumeService(repo, nil)
	ctx := context.Background()
	scope := sandbox.TenantInstanceScope{OrganizationID: "org-a", TenantID: "workspace-a", InstanceID: "inst-a"}

	if _, err := svc.CreateVolume(ctx, CreateVolumeRequest{Scope: scope, SizeGB: 1}); !errors.Is(err, ErrVolumeNameRequired) {
		t.Fatalf("expected name required, got %v", err)
	}
	vol, err := svc.CreateVolume(ctx, CreateVolumeRequest{Scope: scope, Name: " data ", SizeGB: 2})
	if err != nil {
		t.Fatalf("create volume: %v", err)
	}
	if vol.TenantID != "workspace-a" || vol.Name != "data" || vol.SizeBytes != 2*1024*1024*1024 {
		t.Fatalf("unexpected volume: %+v", vol)
	}
	if repo.createdTenantID != "workspace-a" {
		t.Fatalf("unexpected tenant id: %q", repo.createdTenantID)
	}
}

func TestVolumeObjectPrefix(t *testing.T) {
	t.Parallel()

	if got := VolumeObjectPrefix("vol-a"); got != "volumes/vol-a/" {
		t.Fatalf("unexpected prefix: %q", got)
	}
}

func TestNormalizeVolumeMountPath(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{name: "mnt", in: " /mnt/data/ ", want: "/mnt/data"},
		{name: "trooper mounts", in: "/workspace/mounts/cache", want: "/workspace/mounts/cache"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeVolumeMountPath(tc.in)
			if err != nil {
				t.Fatalf("NormalizeVolumeMountPath: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}

	for _, in := range []string{"", "relative", "/", "/etc/secrets", "/mnt/../etc", "/workspace"} {
		if _, err := NormalizeVolumeMountPath(in); !errors.Is(err, ErrVolumeMountPath) {
			t.Fatalf("NormalizeVolumeMountPath(%q) error = %v, want ErrVolumeMountPath", in, err)
		}
	}
}

func TestNormalizeVolumeSubPath(t *testing.T) {
	t.Parallel()

	got, err := NormalizeVolumeSubPath(" data/cache/ ")
	if err != nil {
		t.Fatalf("NormalizeVolumeSubPath: %v", err)
	}
	if got != "data/cache" {
		t.Fatalf("subpath = %q, want data/cache", got)
	}

	got, err = NormalizeVolumeSubPath("")
	if err != nil || got != "" {
		t.Fatalf("empty subpath = %q err=%v, want empty nil", got, err)
	}

	for _, in := range []string{"/absolute", "../escape", "a/../../escape"} {
		if _, err := NormalizeVolumeSubPath(in); !errors.Is(err, ErrVolumeSubPath) {
			t.Fatalf("NormalizeVolumeSubPath(%q) error = %v, want ErrVolumeSubPath", in, err)
		}
	}
}

func TestVolumeObjectSubPath(t *testing.T) {
	t.Parallel()

	if got := VolumeObjectSubPath("/vol-a/"); got != "volumes/vol-a" {
		t.Fatalf("unexpected subpath: %q", got)
	}
}

type fakeVolumeRepo struct {
	createdTenantID string
}

func (r *fakeVolumeRepo) CreateVolume(_ context.Context, tenantID, name string, sizeBytes int64) (*Volume, error) {
	r.createdTenantID = tenantID
	now := time.Now()
	return &Volume{ID: "vol-a", TenantID: tenantID, Name: name, SizeBytes: sizeBytes, CreatedAt: now, UpdatedAt: now}, nil
}

func (r *fakeVolumeRepo) ListVolumes(_ context.Context, tenantID string) ([]Volume, error) {
	now := time.Now()
	return []Volume{{ID: "vol-a", TenantID: tenantID, CreatedAt: now, UpdatedAt: now}}, nil
}

func (r *fakeVolumeRepo) DeleteVolume(_ context.Context, _, _ string) error {
	return nil
}
