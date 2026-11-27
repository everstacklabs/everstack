package snapshot

// Workspace tarball persistence.
//
// When a sandbox sleeps, its /workspace is tarred and uploaded here so the
// workspace survives the gateway pod (whose local disk has no persistent
// volume). Layout, under the same per-tenant prefix as the VM snapshots:
//
//	tenants/{tenant_id}/sandboxes/{sandbox_id}/workspace/{incarnation}/{run_id}/{generation}.tar.gz
//
// Sandbox ids are reused (persistent agents use wks_<agent_id>), so keys
// carry the row's workspace_incarnation (new when a dead row is reused for a
// new sandbox) and workspace_run_id (new each time the sandbox starts
// running). A tarball is therefore bound to the exact run whose stop wrote
// it, and lookups without a ref only consider the current run.
//
// Every sleep writes a new generation key, verifies it, and only then is the
// row allowed to say "sleeping". Once that new generation is verified, every
// other generation for the sandbox is pruned, so one tarball survives, and
// the one consumed by the last wake is kept until the next verified sleep.
//
// Integrity: the uploader hashes the local tarball (SHA-256) before upload,
// declares that digest and size to the Blob Plane multipart upload, and the
// adapter re-reads the completed object and compares it before returning
// (internal/storage/s3 verifyCompletedMultipart). The digest and size are
// also recorded in the WorkspaceRef persisted on the sandbox row, and the
// download path recomputes and compares both before anything is restored.
//
// Encryption: R2 encrypts every object at rest (AES-256) and offers no
// opt-out, so no header is sent. The bucket must stay private; see
// docs/deployment/sandbox-r2-snapshots.md.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/everstacklabs/everstack/internal/storage"
)

// defaultWorkspacePartSize is the multipart part size for workspace
// uploads. It bounds the adapter's per-part local staging file and keeps
// every non-final part the same size, which R2 requires.
const defaultWorkspacePartSize int64 = 64 << 20

// workspaceRefScheme prefixes object-storage workspace refs persisted on
// sandbox rows. Anything without it is a legacy local path or the legacy
// archive marker.
const workspaceRefScheme = "objstore:v1:"

var (
	// ErrWorkspaceCorrupt means the stored tarball does not match the
	// digest or size recorded when it was uploaded.
	ErrWorkspaceCorrupt = errors.New("snapshot: workspace tarball failed checksum verification")
	// ErrWorkspaceRefForeign means a ref points outside the requesting
	// tenant's and sandbox's own workspace prefix.
	ErrWorkspaceRefForeign = errors.New("snapshot: workspace ref does not belong to this sandbox")
	// ErrInvalidWorkspaceOwner means a tenant or sandbox id is not a safe
	// single key segment.
	ErrInvalidWorkspaceOwner = errors.New("snapshot: tenant and sandbox ids must be single safe key segments")
	// ErrWorkspaceStoreUnsupported means the configured object store lacks
	// the verified (Blob Plane V2) operations the workspace path needs.
	ErrWorkspaceStoreUnsupported = errors.New("snapshot: object store does not support verified workspace uploads")
)

// WorkspaceRef identifies one uploaded workspace tarball and the integrity
// data needed to verify it on download.
type WorkspaceRef struct {
	Key       string
	SHA256    string // lowercase hex
	SizeBytes int64
}

// String encodes the ref for the sandbox row's workspace ref column.
func (r WorkspaceRef) String() string {
	return workspaceRefScheme + r.Key + "#sha256=" + r.SHA256 + "&size=" + strconv.FormatInt(r.SizeBytes, 10)
}

// ParseWorkspaceRef decodes a persisted ref. ok is false when s is not an
// object-storage workspace ref at all (a legacy local path or marker); err
// is set when it is one but is malformed.
func ParseWorkspaceRef(s string) (ref WorkspaceRef, ok bool, err error) {
	if !strings.HasPrefix(s, workspaceRefScheme) {
		return WorkspaceRef{}, false, nil
	}
	rest := strings.TrimPrefix(s, workspaceRefScheme)
	k, frag, found := strings.Cut(rest, "#")
	if !found || k == "" {
		return WorkspaceRef{}, true, fmt.Errorf("snapshot: malformed workspace ref")
	}
	values, err := url.ParseQuery(frag)
	if err != nil {
		return WorkspaceRef{}, true, fmt.Errorf("snapshot: malformed workspace ref: %w", err)
	}
	sum := values.Get("sha256")
	decoded, hexErr := hex.DecodeString(sum)
	if hexErr != nil || len(decoded) != sha256.Size || sum != strings.ToLower(sum) {
		return WorkspaceRef{}, true, fmt.Errorf("snapshot: malformed workspace ref digest")
	}
	size, sizeErr := strconv.ParseInt(values.Get("size"), 10, 64)
	if sizeErr != nil || size <= 0 {
		return WorkspaceRef{}, true, fmt.Errorf("snapshot: malformed workspace ref size")
	}
	return WorkspaceRef{Key: k, SHA256: sum, SizeBytes: size}, true, nil
}

// IsWorkspaceObjectRef reports whether s is an object-storage workspace ref
// (well-formed or not).
func IsWorkspaceObjectRef(s string) bool { return strings.HasPrefix(s, workspaceRefScheme) }

// WorkspacePrefix is the per-tenant, per-sandbox key prefix every workspace
// tarball lives under. It ends in a slash.
func WorkspacePrefix(tenantID, sandboxID string) string {
	return sandboxPrefix(tenantID, sandboxID) + "/workspace/"
}

// WorkspaceOwner identifies whose workspace a tarball is: the tenant, the
// sandbox id, the incarnation of that id, and the run (VM lifetime from
// create or wake to the next stop).
type WorkspaceOwner struct {
	TenantID    string
	SandboxID   string
	Incarnation string
	RunID       string
}

// IncarnationPrefix is the key prefix for one incarnation's tarballs.
func IncarnationPrefix(o WorkspaceOwner) string {
	return WorkspacePrefix(o.TenantID, o.SandboxID) + o.Incarnation + "/"
}

// RunPrefix is the key prefix for one run's tarballs.
func RunPrefix(o WorkspaceOwner) string {
	return IncarnationPrefix(o) + o.RunID + "/"
}

func (o WorkspaceOwner) validate(needRun bool) error {
	if err := validateOwner(o.TenantID, o.SandboxID); err != nil {
		return err
	}
	if err := validateOwnerSegment(o.Incarnation); err != nil {
		return err
	}
	if needRun {
		return validateOwnerSegment(o.RunID)
	}
	return nil
}

// WorkspaceStore is implemented by stores that can persist workspace
// tarballs with verification. The Disabled store deliberately does not
// implement it: callers use that to detect "persistence not configured".
type WorkspaceStore interface {
	// PutWorkspace uploads the local tarball under a new generation key in
	// the owner's run and returns its ref only after the stored bytes have
	// been verified against the local SHA-256 and size.
	PutWorkspace(ctx context.Context, owner WorkspaceOwner, localPath string) (WorkspaceRef, error)
	// GetWorkspace streams the tarball into dst, verifying that the ref
	// belongs to the owner's current incarnation, its size and SHA-256.
	// Returns ErrSnapshotMissing when the object is gone and
	// ErrWorkspaceCorrupt on any integrity mismatch. dst may hold partial
	// data on error; callers must discard it.
	GetWorkspace(ctx context.Context, owner WorkspaceOwner, ref WorkspaceRef, dst io.Writer) error
	// PruneWorkspaces deletes every workspace tarball for the sandbox other
	// than keep, including the legacy archive key. A zero keep deletes all.
	PruneWorkspaces(ctx context.Context, tenantID, sandboxID string, keep WorkspaceRef) error
	// LatestWorkspace finds the newest generation written in the owner's
	// current run, streams it into dst while verifying it against the
	// SHA-256 and size recorded in its object metadata, and returns its
	// ref. found is false when the run has no generation. An object that
	// cannot be verified returns ErrWorkspaceCorrupt.
	LatestWorkspace(ctx context.Context, owner WorkspaceOwner, dst io.Writer) (ref WorkspaceRef, found bool, err error)
	// HasWorkspace reports whether the owner's current run has a
	// generation (a list call, no download).
	HasWorkspace(ctx context.Context, owner WorkspaceOwner) (bool, error)
	// DeleteIncarnation deletes every tarball of one incarnation.
	DeleteIncarnation(ctx context.Context, owner WorkspaceOwner) error
}

func validateOwnerSegment(s string) error {
	if s == "" || s == "." || s == ".." {
		return ErrInvalidWorkspaceOwner
	}
	for _, r := range s {
		if r == '/' || r == '\\' || unicode.IsSpace(r) || unicode.IsControl(r) {
			return ErrInvalidWorkspaceOwner
		}
	}
	return nil
}

func validateOwner(tenantID, sandboxID string) error {
	if err := validateOwnerSegment(tenantID); err != nil {
		return err
	}
	return validateOwnerSegment(sandboxID)
}

func (s *ObjectStoreBacked) blobPlane() (storage.BlobPlane, error) {
	blob, err := storage.RequireBlobPlane(s.store)
	if err != nil {
		return nil, ErrWorkspaceStoreUnsupported
	}
	return blob, nil
}

func newGenerationKey(owner WorkspaceOwner) (string, error) {
	var nonce [6]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("snapshot: generation nonce: %w", err)
	}
	gen := time.Now().UTC().Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(nonce[:])
	return RunPrefix(owner) + gen + ".tar.gz", nil
}

func hashFile(f *os.File) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// PutWorkspace implements WorkspaceStore.
func (s *ObjectStoreBacked) PutWorkspace(ctx context.Context, owner WorkspaceOwner, localPath string) (WorkspaceRef, error) {
	if err := owner.validate(true); err != nil {
		return WorkspaceRef{}, err
	}
	blob, err := s.blobPlane()
	if err != nil {
		return WorkspaceRef{}, err
	}
	f, err := os.Open(localPath)
	if err != nil {
		return WorkspaceRef{}, fmt.Errorf("snapshot: open workspace tarball: %w", err)
	}
	defer f.Close()
	sum, size, err := hashFile(f)
	if err != nil {
		return WorkspaceRef{}, fmt.Errorf("snapshot: hash workspace tarball: %w", err)
	}
	if size <= 0 {
		return WorkspaceRef{}, fmt.Errorf("snapshot: workspace tarball is empty")
	}
	objectKey, err := newGenerationKey(owner)
	if err != nil {
		return WorkspaceRef{}, err
	}
	checksum := storage.Checksum{Algorithm: storage.ChecksumSHA256, Value: sum}
	upload, err := blob.BeginMultipart(ctx, s.bucket, objectKey, storage.MultipartOptions{
		ContentType:  "application/gzip",
		ExpectedSize: &size,
		Checksum:     checksum,
	})
	if err != nil {
		return WorkspaceRef{}, fmt.Errorf("snapshot: begin workspace upload: %w", err)
	}
	abort := func() {
		// Fresh context: the caller's may already be cancelled.
		abortCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = blob.AbortMultipart(abortCtx, upload)
	}

	partSize := s.workspacePartSize
	if partSize <= 0 {
		partSize = defaultWorkspacePartSize
	}
	var parts []storage.UploadedPart
	for offset, number := int64(0), int32(1); offset < size; offset, number = offset+partSize, number+1 {
		n := partSize
		if remaining := size - offset; remaining < n {
			n = remaining
		}
		part, err := blob.UploadPart(ctx, upload, number, io.NewSectionReader(f, offset, n), storage.PutOptions{ExpectedSize: &n})
		if err != nil {
			abort()
			return WorkspaceRef{}, fmt.Errorf("snapshot: upload workspace part %d: %w", number, err)
		}
		parts = append(parts, part)
	}

	// CompleteMultipart re-reads the stored object and compares it against
	// the declared digest and size; a mismatch deletes the object and
	// returns a checksum error.
	info, err := blob.CompleteMultipart(ctx, upload, parts)
	if err != nil {
		if storage.IsCode(err, storage.ErrorChecksumMismatch) {
			return WorkspaceRef{}, fmt.Errorf("%w: %v", ErrWorkspaceCorrupt, err)
		}
		abort()
		return WorkspaceRef{}, fmt.Errorf("snapshot: complete workspace upload: %w", err)
	}
	if info.SizeBytes != size || info.Checksum.Value != sum {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = blob.Delete(cleanupCtx, s.bucket, objectKey)
		cancel()
		return WorkspaceRef{}, fmt.Errorf("%w: stored object did not match the uploaded tarball", ErrWorkspaceCorrupt)
	}
	return WorkspaceRef{Key: objectKey, SHA256: sum, SizeBytes: size}, nil
}

// keyUnder reports whether key is exactly depth segments below prefix,
// with no empty, dot or traversal segments.
func keyUnder(key, prefix string, depth int) bool {
	if !strings.HasPrefix(key, prefix) || path.Clean(key) != key {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(key, prefix), "/")
	if len(parts) != depth {
		return false
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." || strings.Contains(p, "\\") {
			return false
		}
	}
	return true
}

// refOwnedBy reports whether ref belongs to the owner's current
// incarnation (any run of it): <incarnation prefix>/<run>/<name>.
func refOwnedBy(ref WorkspaceRef, owner WorkspaceOwner) bool {
	return keyUnder(ref.Key, IncarnationPrefix(owner), 2)
}

// GetWorkspace implements WorkspaceStore.
func (s *ObjectStoreBacked) GetWorkspace(ctx context.Context, owner WorkspaceOwner, ref WorkspaceRef, dst io.Writer) error {
	if err := owner.validate(false); err != nil {
		return err
	}
	if !refOwnedBy(ref, owner) {
		return ErrWorkspaceRefForeign
	}
	blob, err := s.blobPlane()
	if err != nil {
		return err
	}
	obj, err := blob.Get(ctx, s.bucket, ref.Key)
	if err != nil {
		if storage.IsCode(err, storage.ErrorNotFound) {
			return ErrSnapshotMissing
		}
		if storage.IsCode(err, storage.ErrorChecksumMismatch) {
			return fmt.Errorf("%w: %v", ErrWorkspaceCorrupt, err)
		}
		return fmt.Errorf("snapshot: get workspace: %w", err)
	}
	defer obj.Body.Close()

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, h), io.LimitReader(obj.Body, ref.SizeBytes+1))
	if err != nil {
		if storage.IsCode(err, storage.ErrorChecksumMismatch) {
			return fmt.Errorf("%w: %v", ErrWorkspaceCorrupt, err)
		}
		return fmt.Errorf("snapshot: read workspace: %w", err)
	}
	if n != ref.SizeBytes {
		return fmt.Errorf("%w: size %d, recorded %d", ErrWorkspaceCorrupt, n, ref.SizeBytes)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != ref.SHA256 {
		return fmt.Errorf("%w: digest differs from the recorded upload", ErrWorkspaceCorrupt)
	}
	return nil
}

// PruneWorkspaces implements WorkspaceStore.
func (s *ObjectStoreBacked) PruneWorkspaces(ctx context.Context, tenantID, sandboxID string, keep WorkspaceRef) error {
	if err := validateOwner(tenantID, sandboxID); err != nil {
		return err
	}
	objects, err := s.store.List(ctx, s.bucket, WorkspacePrefix(tenantID, sandboxID))
	if err != nil {
		return fmt.Errorf("snapshot: list workspaces: %w", err)
	}
	var firstErr error
	for _, obj := range objects {
		if obj.Key == keep.Key {
			continue
		}
		if err := s.store.Delete(ctx, s.bucket, obj.Key); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	// Legacy archive key written by the pre-generation archive path.
	if err := s.store.Delete(ctx, s.bucket, key(tenantID, sandboxID, KindWorkspace)); err != nil &&
		!storage.IsCode(err, storage.ErrorNotFound) && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// listRunGenerations returns the generation keys of the owner's current
// run, newest first. Generation names start with a fixed-width UTC
// timestamp, so lexical order is chronological.
func (s *ObjectStoreBacked) listRunGenerations(ctx context.Context, owner WorkspaceOwner) ([]string, error) {
	if err := owner.validate(true); err != nil {
		return nil, err
	}
	prefix := RunPrefix(owner)
	objects, err := s.store.List(ctx, s.bucket, prefix)
	if err != nil {
		return nil, fmt.Errorf("snapshot: list workspaces: %w", err)
	}
	keys := make([]string, 0, len(objects))
	for _, obj := range objects {
		if strings.HasSuffix(obj.Key, ".tar.gz") && keyUnder(obj.Key, prefix, 1) {
			keys = append(keys, obj.Key)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	return keys, nil
}

// HasWorkspace implements WorkspaceStore.
func (s *ObjectStoreBacked) HasWorkspace(ctx context.Context, owner WorkspaceOwner) (bool, error) {
	keys, err := s.listRunGenerations(ctx, owner)
	if err != nil {
		return false, err
	}
	return len(keys) > 0, nil
}

// DeleteIncarnation implements WorkspaceStore.
func (s *ObjectStoreBacked) DeleteIncarnation(ctx context.Context, owner WorkspaceOwner) error {
	if err := owner.validate(false); err != nil {
		return err
	}
	objects, err := s.store.List(ctx, s.bucket, IncarnationPrefix(owner))
	if err != nil {
		return fmt.Errorf("snapshot: list incarnation: %w", err)
	}
	var firstErr error
	for _, obj := range objects {
		if err := s.store.Delete(ctx, s.bucket, obj.Key); err != nil && !storage.IsCode(err, storage.ErrorNotFound) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// LatestWorkspace implements WorkspaceStore.
func (s *ObjectStoreBacked) LatestWorkspace(ctx context.Context, owner WorkspaceOwner, dst io.Writer) (WorkspaceRef, bool, error) {
	keys, err := s.listRunGenerations(ctx, owner)
	if err != nil {
		return WorkspaceRef{}, false, err
	}
	if len(keys) == 0 {
		return WorkspaceRef{}, false, nil
	}
	blob, err := s.blobPlane()
	if err != nil {
		return WorkspaceRef{}, false, err
	}
	obj, err := blob.Get(ctx, s.bucket, keys[0])
	if err != nil {
		if storage.IsCode(err, storage.ErrorNotFound) {
			return WorkspaceRef{}, false, ErrSnapshotMissing
		}
		if storage.IsCode(err, storage.ErrorChecksumMismatch) {
			return WorkspaceRef{}, false, fmt.Errorf("%w: %v", ErrWorkspaceCorrupt, err)
		}
		return WorkspaceRef{}, false, fmt.Errorf("snapshot: get workspace: %w", err)
	}
	defer obj.Body.Close()
	if obj.Checksum.IsZero() || obj.Checksum.Validate() != nil {
		return WorkspaceRef{}, false, fmt.Errorf("%w: newest generation has no recorded digest", ErrWorkspaceCorrupt)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, h), obj.Body)
	if err != nil {
		if storage.IsCode(err, storage.ErrorChecksumMismatch) {
			return WorkspaceRef{}, false, fmt.Errorf("%w: %v", ErrWorkspaceCorrupt, err)
		}
		return WorkspaceRef{}, false, fmt.Errorf("snapshot: read workspace: %w", err)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if sum != obj.Checksum.Value || (obj.SizeBytes > 0 && n != obj.SizeBytes) || n <= 0 {
		return WorkspaceRef{}, false, fmt.Errorf("%w: newest generation does not match its recorded digest", ErrWorkspaceCorrupt)
	}
	return WorkspaceRef{Key: keys[0], SHA256: sum, SizeBytes: n}, true, nil
}

var _ WorkspaceStore = (*ObjectStoreBacked)(nil)
