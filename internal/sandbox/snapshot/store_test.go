package snapshot

import (
	"context"
	"errors"
	"testing"
)

func TestKeyLayout(t *testing.T) {
	tests := []struct {
		name      string
		tenant    string
		sandbox   string
		kind      Kind
		wantKey   string
		wantPfx   string
		wantMani  string
	}{
		{
			name:     "happy path",
			tenant:   "tnt_1",
			sandbox:  "sbx_abc",
			kind:     KindMemory,
			wantKey:  "tenants/tnt_1/sandboxes/sbx_abc/memory.bin",
			wantPfx:  "tenants/tnt_1/sandboxes/sbx_abc",
			wantMani: "tenants/tnt_1/sandboxes/sbx_abc/manifest.json",
		},
		{
			name:     "rootfs key",
			tenant:   "t",
			sandbox:  "s",
			kind:     KindRootfs,
			wantKey:  "tenants/t/sandboxes/s/rootfs.img",
			wantPfx:  "tenants/t/sandboxes/s",
			wantMani: "tenants/t/sandboxes/s/manifest.json",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := key(tc.tenant, tc.sandbox, tc.kind); got != tc.wantKey {
				t.Errorf("key: got %q want %q", got, tc.wantKey)
			}
			if got := sandboxPrefix(tc.tenant, tc.sandbox); got != tc.wantPfx {
				t.Errorf("prefix: got %q want %q", got, tc.wantPfx)
			}
			if got := manifestKey(tc.tenant, tc.sandbox); got != tc.wantMani {
				t.Errorf("manifest: got %q want %q", got, tc.wantMani)
			}
		})
	}
}

func TestDisabledStoreBehavior(t *testing.T) {
	s := NewDisabled()
	ctx := context.Background()

	if rec, err := s.PutStream(ctx, "t", "s", KindRootfs, "application/octet-stream", nil); err != nil {
		t.Errorf("PutStream should no-op, got %v rec=%+v", err, rec)
	}
	if err := s.PutManifest(ctx, Manifest{TenantID: "t", SandboxID: "s"}); err != nil {
		t.Errorf("PutManifest should no-op, got %v", err)
	}
	if _, err := s.GetManifest(ctx, "t", "s"); !errors.Is(err, ErrSnapshotMissing) {
		t.Errorf("GetManifest: want ErrSnapshotMissing, got %v", err)
	}
	if _, err := s.GetStream(ctx, "t", "s", KindRootfs); !errors.Is(err, ErrSnapshotMissing) {
		t.Errorf("GetStream: want ErrSnapshotMissing, got %v", err)
	}
	if err := s.Delete(ctx, "t", "s"); err != nil {
		t.Errorf("Delete should no-op, got %v", err)
	}
	if objs, err := s.ListByTenant(ctx, "t"); err != nil || len(objs) != 0 {
		t.Errorf("ListByTenant: want nil/empty, got %v/%v", objs, err)
	}
}

func TestPutStreamRejectsMissingIDs(t *testing.T) {
	s := NewFromObjectStore(nil, "bkt")
	if _, err := s.PutStream(context.Background(), "", "sbx", KindRootfs, "", nil); err == nil {
		t.Errorf("PutStream should reject empty tenantID")
	}
	if _, err := s.PutStream(context.Background(), "tnt", "", KindRootfs, "", nil); err == nil {
		t.Errorf("PutStream should reject empty sandboxID")
	}
}

func TestPutManifestRequiresIDs(t *testing.T) {
	s := NewFromObjectStore(nil, "bkt")
	if err := s.PutManifest(context.Background(), Manifest{}); err == nil {
		t.Errorf("PutManifest should reject empty ids")
	}
}
