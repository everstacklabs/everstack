// Package snapshot persists firecracker microVM snapshots (memory + state +
// rootfs) to object storage so a sandbox can survive host loss. Postgres
// remains the durable source of truth for the *catalog* of sandboxes;
// object storage is the source of truth for the *contents* needed to
// revive one on a different host.
//
// Layout under a single shared bucket:
//
//	tenants/{tenant_id}/sandboxes/{sandbox_id}/manifest.json
//	tenants/{tenant_id}/sandboxes/{sandbox_id}/state.json
//	tenants/{tenant_id}/sandboxes/{sandbox_id}/memory.bin
//	tenants/{tenant_id}/sandboxes/{sandbox_id}/rootfs.img
//
// Isolation is enforced at the prefix level by the surrounding policy
// (bucket IAM + this package only ever computing keys from the caller's
// tenant ID). Each backend adapter chooses which kinds to upload —
// firecracker writes all four; future docker adapter may only write
// rootfs.img + manifest.
package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/storage"
)

// Kind identifies one of the streams that make up a sandbox snapshot.
type Kind string

const (
	// KindState is the JSON-encoded backend-specific resume metadata
	// (e.g. firecracker's microVM state file). Small (KB-range).
	KindState Kind = "state.json"
	// KindMemory is the VM RAM snapshot. Sized roughly to the
	// configured memory limit (MB-range, sometimes GB).
	KindMemory Kind = "memory.bin"
	// KindRootfs is the disk image / overlay tarball. Largest stream
	// (GB-range typically).
	KindRootfs Kind = "rootfs.img"
	// KindWorkspace is the gzipped /workspace workspace tarball uploaded
	// when a sleeping sandbox is ARCHIVED: the local tar is moved to
	// object storage and deleted from host disk. Restore-from-archived
	// downloads this stream back before the normal revive restore.
	KindWorkspace Kind = "workspace.tar.gz"
)

// Manifest is the per-sandbox index file that records what got uploaded
// for a snapshot, its size, and when it was taken. It's the first thing
// the restore path fetches: if no manifest exists, there's nothing to
// restore. Manifests are also what the GC walks.
type Manifest struct {
	SandboxID  string         `json:"sandbox_id"`
	TenantID string         `json:"tenant_id"`
	AgentID  string         `json:"agent_id,omitempty"`
	Backend  string         `json:"backend"`
	TakenAt  time.Time      `json:"taken_at"`
	Trigger  string         `json:"trigger,omitempty"` // "sleep", "periodic", "shutdown"
	Streams  []StreamRecord `json:"streams"`
	// Network captures the guest-visible network identity at snapshot
	// time so a restore on a different host can recreate the same MAC
	// and iface_id before load_snapshot. Nil when the sandbox was in
	// NetworkDeny mode.
	Network *NetworkSpec `json:"network,omitempty"`
	// Encryption hints for the restore path. Empty when relying purely
	// on R2 server-side encryption (the default for this phase).
	Encryption string `json:"encryption,omitempty"`
}

// NetworkSpec is the subset of the firecracker NetworkConfig that the
// restore path needs in order to recreate the guest-visible network
// identity. The host-side TAP name is intentionally NOT included — a
// fresh one is allocated on the restoring host. What matters is the
// guest's view: the MAC must match the snapshot or the guest kernel
// will see the interface as flapped and applications may stall.
type NetworkSpec struct {
	IfaceID    string   `json:"iface_id"`  // logical name in the firecracker config (typically "eth0")
	GuestMAC   string   `json:"guest_mac"` // pinned across snapshot/restore
	GuestIP    string   `json:"guest_ip,omitempty"`
	HostIP     string   `json:"host_ip,omitempty"`
	MTU        int      `json:"mtu,omitempty"`
	DNSServers []string `json:"dns_servers,omitempty"`
}

// StreamRecord captures the per-kind upload metadata so the restore
// path can verify integrity before booting.
type StreamRecord struct {
	Kind        Kind   `json:"kind"`
	SizeBytes   int64  `json:"size_bytes"`
	ETag        string `json:"etag,omitempty"`
	ContentType string `json:"content_type,omitempty"`
}

// Store is the high-level API the rest of the sandbox code uses. It
// hides the bucket + prefix layout and the underlying object store
// implementation so callers can be backend-agnostic.
type Store interface {
	// PutStream uploads a single snapshot stream for sandboxID. Reader
	// is consumed and the returned StreamRecord is suitable for
	// inclusion in a Manifest.
	PutStream(ctx context.Context, tenantID, sandboxID string, kind Kind, contentType string, body io.Reader) (StreamRecord, error)
	// PutManifest writes the manifest.json. Should be called *after*
	// all PutStream calls so an in-progress upload never resembles a
	// completed snapshot to the restore path.
	PutManifest(ctx context.Context, m Manifest) error
	// GetManifest fetches the manifest if present. Returns
	// ErrSnapshotMissing when no snapshot exists for this sandbox.
	GetManifest(ctx context.Context, tenantID, sandboxID string) (*Manifest, error)
	// GetStream opens a streaming reader for a single kind. Caller
	// must Close the returned ReadCloser.
	GetStream(ctx context.Context, tenantID, sandboxID string, kind Kind) (io.ReadCloser, error)
	// Delete removes every object under the sandbox's prefix
	// (manifest + all streams). Idempotent.
	Delete(ctx context.Context, tenantID, sandboxID string) error
	// ListByTenant returns the manifests visible under a tenant
	// prefix. Used by the GC sweep.
	ListByTenant(ctx context.Context, tenantID string) ([]storage.BucketObject, error)
}

// ErrSnapshotMissing is returned by GetManifest when no snapshot has
// been taken for the requested sandbox.
var ErrSnapshotMissing = errors.New("snapshot: not found")

// ---------------------------------------------------------------------------
// Disabled — a Store that refuses every operation cleanly.
// ---------------------------------------------------------------------------

// Disabled is the Store used when R2 is not configured. Save calls
// silently no-op so backends can call PutStream/PutManifest
// unconditionally; reads return ErrSnapshotMissing.
type Disabled struct{}

// NewDisabled returns a no-op Store.
func NewDisabled() *Disabled { return &Disabled{} }

func (Disabled) PutStream(context.Context, string, string, Kind, string, io.Reader) (StreamRecord, error) {
	return StreamRecord{}, nil
}
func (Disabled) PutManifest(context.Context, Manifest) error { return nil }
func (Disabled) GetManifest(context.Context, string, string) (*Manifest, error) {
	return nil, ErrSnapshotMissing
}
func (Disabled) GetStream(context.Context, string, string, Kind) (io.ReadCloser, error) {
	return nil, ErrSnapshotMissing
}
func (Disabled) Delete(context.Context, string, string) error { return nil }
func (Disabled) ListByTenant(context.Context, string) ([]storage.BucketObject, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// ObjectStoreBacked — real Store on top of storage.ObjectStore.
// ---------------------------------------------------------------------------

// ObjectStoreBacked implements Store on top of any storage.ObjectStore.
// The same code path serves R2, S3, MinIO — only the wiring differs.
type ObjectStoreBacked struct {
	store  storage.ObjectStore
	bucket string
}

// NewFromObjectStore returns a Store that writes snapshots into the
// given bucket via the supplied ObjectStore. Bucket may be empty if
// the underlying store already has a default configured.
func NewFromObjectStore(store storage.ObjectStore, bucket string) *ObjectStoreBacked {
	return &ObjectStoreBacked{store: store, bucket: bucket}
}

func sandboxPrefix(tenantID, sandboxID string) string {
	// Use path.Join semantics rather than fmt to keep separators sane
	// across platforms even though S3 keys are always forward-slash.
	return strings.TrimSuffix(
		path.Join("tenants", tenantID, "sandboxes", sandboxID),
		"/",
	)
}

func key(tenantID, sandboxID string, kind Kind) string {
	return sandboxPrefix(tenantID, sandboxID) + "/" + string(kind)
}

func manifestKey(tenantID, sandboxID string) string {
	return sandboxPrefix(tenantID, sandboxID) + "/manifest.json"
}

func (s *ObjectStoreBacked) PutStream(ctx context.Context, tenantID, sandboxID string, kind Kind, contentType string, body io.Reader) (StreamRecord, error) {
	if tenantID == "" || sandboxID == "" {
		return StreamRecord{}, errors.New("snapshot: tenantID and sandboxID required")
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	counted := &countingReader{r: body}
	etag, err := s.store.Put(ctx, s.bucket, key(tenantID, sandboxID, kind), contentType, counted)
	if err != nil {
		return StreamRecord{}, fmt.Errorf("snapshot: put %s/%s: %w", sandboxID, kind, err)
	}
	return StreamRecord{
		Kind:        kind,
		SizeBytes:   counted.n,
		ETag:        etag,
		ContentType: contentType,
	}, nil
}

func (s *ObjectStoreBacked) PutManifest(ctx context.Context, m Manifest) error {
	if m.TenantID == "" || m.SandboxID == "" {
		return errors.New("snapshot: manifest TenantID and SandboxID required")
	}
	if m.TakenAt.IsZero() {
		m.TakenAt = time.Now().UTC()
	}
	payload, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("snapshot: marshal manifest: %w", err)
	}
	if _, err := s.store.Put(ctx, s.bucket, manifestKey(m.TenantID, m.SandboxID), "application/json", bytesReader(payload)); err != nil {
		return fmt.Errorf("snapshot: put manifest: %w", err)
	}
	return nil
}

func (s *ObjectStoreBacked) GetManifest(ctx context.Context, tenantID, sandboxID string) (*Manifest, error) {
	rc, err := s.GetStream(ctx, tenantID, sandboxID, Kind("manifest.json"))
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	body, err := io.ReadAll(io.LimitReader(rc, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("snapshot: read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("snapshot: decode manifest: %w", err)
	}
	return &m, nil
}

func (s *ObjectStoreBacked) GetStream(ctx context.Context, tenantID, sandboxID string, kind Kind) (io.ReadCloser, error) {
	// We synthesise a streaming reader from a presigned URL so we
	// don't buffer multi-GB rootfs in memory. The ObjectStore
	// interface only exposes presigned GET today; ranged reads or a
	// direct GetObject would be a future enhancement.
	signedURL, err := s.store.GetPresignedURL(ctx, s.bucket, key(tenantID, sandboxID, kind), 10*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("snapshot: presign %s/%s: %w", sandboxID, kind, err)
	}
	resp, err := defaultHTTPDo(ctx, signedURL)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 404 {
		resp.Body.Close()
		return nil, ErrSnapshotMissing
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, fmt.Errorf("snapshot: get %s/%s: status %d", sandboxID, kind, resp.StatusCode)
	}
	return resp.Body, nil
}

func (s *ObjectStoreBacked) Delete(ctx context.Context, tenantID, sandboxID string) error {
	if tenantID == "" || sandboxID == "" {
		return errors.New("snapshot: tenantID and sandboxID required")
	}
	// Best-effort across known kinds + manifest.
	keys := []string{
		manifestKey(tenantID, sandboxID),
		key(tenantID, sandboxID, KindState),
		key(tenantID, sandboxID, KindMemory),
		key(tenantID, sandboxID, KindRootfs),
	}
	var firstErr error
	for _, k := range keys {
		if err := s.store.Delete(ctx, s.bucket, k); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *ObjectStoreBacked) ListByTenant(ctx context.Context, tenantID string) ([]storage.BucketObject, error) {
	if tenantID == "" {
		return nil, errors.New("snapshot: tenantID required")
	}
	return s.store.List(ctx, s.bucket, path.Join("tenants", tenantID, "sandboxes")+"/")
}

// Compile-time check.
var _ Store = (*ObjectStoreBacked)(nil)
var _ Store = (*Disabled)(nil)
