// Package managed resolves internal Everstack Storage placements to physical
// object stores without exposing cell configuration to tenant-facing APIs.
package managed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	storagepkg "github.com/everstacklabs/everstack/internal/storage"
)

var (
	// ErrCellUnavailable means a placement references no configured storage cell.
	ErrCellUnavailable = errors.New("managed storage cell is unavailable")
	// ErrInvalidPlacement means persisted placement metadata is incomplete or
	// does not match the tenant's deterministic namespace.
	ErrInvalidPlacement = errors.New("managed storage placement is invalid")
	// ErrTenantScopeViolation means a provider returned an object outside the
	// tenant prefix that was used for the request.
	ErrTenantScopeViolation = errors.New("managed storage tenant scope violation")
)

// Cell binds one stable placement ID to a platform-configured object store.
// The store owns the physical endpoint, bucket and credentials.
type Cell struct {
	ID     string
	Bucket string
	Store  storagepkg.ObjectStore
}

type configuredCell struct {
	bucket string
	store  storagepkg.BlobPlane
}

// Resolver selects a configured storage cell and adds a non-overridable tenant
// namespace around every object operation.
type Resolver struct {
	cells map[string]configuredCell
}

// NewResolver constructs a resolver from one or more independently configured
// cells. Cell IDs are immutable placement identities, not provider names.
func NewResolver(cells ...Cell) (*Resolver, error) {
	if len(cells) == 0 {
		return nil, errors.New("at least one managed storage cell is required")
	}

	configured := make(map[string]configuredCell, len(cells))
	for _, cell := range cells {
		cellID := strings.TrimSpace(cell.ID)
		if cellID == "" {
			return nil, errors.New("managed storage cell ID is required")
		}
		bucket := strings.TrimSpace(cell.Bucket)
		if bucket == "" {
			return nil, errors.New("managed storage cell bucket is required")
		}
		blobStore, err := storagepkg.RequireBlobPlane(cell.Store)
		if err != nil {
			return nil, errors.New("managed storage cell must support Blob Plane V2")
		}
		if _, exists := configured[cellID]; exists {
			return nil, errors.New("managed storage cell IDs must be unique")
		}
		configured[cellID] = configuredCell{bucket: bucket, store: blobStore}
	}

	return &Resolver{cells: configured}, nil
}

// ResolveManagedStore selects the persisted cell and verifies that its prefix
// is exactly the deterministic namespace for the persisted tenant.
func (r *Resolver) ResolveManagedStore(_ context.Context, connection storagepkg.ManagedConnection) (storagepkg.ObjectStore, error) {
	if r == nil {
		return nil, ErrCellUnavailable
	}
	tenantID := strings.TrimSpace(connection.TenantID)
	cellID := strings.TrimSpace(connection.CellID)
	prefix := strings.TrimSpace(connection.PathPrefix)
	if tenantID == "" || cellID == "" || prefix == "" || prefix != connection.PathPrefix {
		return nil, ErrInvalidPlacement
	}
	if prefix != storagepkg.ManagedTenantPrefix(tenantID) {
		return nil, ErrInvalidPlacement
	}

	cell, ok := r.cells[cellID]
	if !ok {
		return nil, ErrCellUnavailable
	}
	return &tenantStore{base: cell.store, bucket: cell.bucket, prefix: prefix}, nil
}

type tenantStore struct {
	base   storagepkg.BlobPlane
	bucket string
	prefix string
}

func (s *tenantStore) scopedKey(key string) string {
	return s.prefix + "/" + key
}

func (s *tenantStore) PutPresignedURL(ctx context.Context, _ string, key, contentType string, sizeBytes int64, expiry time.Duration) (string, map[string]string, error) {
	return s.base.PutPresignedURL(ctx, s.bucket, s.scopedKey(key), contentType, sizeBytes, expiry)
}

func (s *tenantStore) GetPresignedURL(ctx context.Context, _ string, key string, expiry time.Duration) (string, error) {
	return s.base.GetPresignedURL(ctx, s.bucket, s.scopedKey(key), expiry)
}

func (s *tenantStore) Put(ctx context.Context, _ string, key, contentType string, body io.Reader) (string, error) {
	return s.base.Put(ctx, s.bucket, s.scopedKey(key), contentType, body)
}

func (s *tenantStore) Delete(ctx context.Context, _ string, key string) error {
	return s.base.Delete(ctx, s.bucket, s.scopedKey(key))
}

func (s *tenantStore) Head(ctx context.Context, _ string, key string) (int64, string, error) {
	return s.base.Head(ctx, s.bucket, s.scopedKey(key))
}

func (s *tenantStore) List(ctx context.Context, _ string, prefix string) ([]storagepkg.BucketObject, error) {
	objects, err := s.base.List(ctx, s.bucket, s.scopedKey(prefix))
	if err != nil {
		return nil, err
	}

	boundary := s.prefix + "/"
	result := make([]storagepkg.BucketObject, 0, len(objects))
	for _, object := range objects {
		if !strings.HasPrefix(object.Key, boundary) {
			return nil, fmt.Errorf("%w: provider returned an out-of-scope object", ErrTenantScopeViolation)
		}
		object.Key = strings.TrimPrefix(object.Key, boundary)
		result = append(result, object)
	}
	return result, nil
}

func (s *tenantStore) BlobCapabilities() storagepkg.BlobCapabilities {
	return s.base.BlobCapabilities()
}

func (s *tenantStore) Get(ctx context.Context, _ string, key string) (*storagepkg.ObjectReader, error) {
	reader, err := s.base.Get(ctx, s.bucket, s.scopedKey(key))
	if err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, fmt.Errorf("%w: provider returned an empty object", ErrTenantScopeViolation)
	}
	logicalKey, err := s.logicalKey(reader.Key)
	if err != nil {
		if reader.Body != nil {
			_ = reader.Body.Close()
		}
		return nil, err
	}
	reader.Key = logicalKey
	return reader, nil
}

func (s *tenantStore) ListPage(ctx context.Context, _ string, options storagepkg.ListOptions) (storagepkg.ObjectPage, error) {
	physicalOptions := options
	physicalOptions.Prefix = s.scopedKey(options.Prefix)
	page, err := s.base.ListPage(ctx, s.bucket, physicalOptions)
	if err != nil {
		return storagepkg.ObjectPage{}, err
	}
	for index := range page.Objects {
		page.Objects[index].Key, err = s.logicalKey(page.Objects[index].Key)
		if err != nil {
			return storagepkg.ObjectPage{}, err
		}
	}
	return page, nil
}

func (s *tenantStore) PutIfAbsent(ctx context.Context, _ string, key string, body io.Reader, options storagepkg.PutOptions) (storagepkg.ObjectInfo, error) {
	info, err := s.base.PutIfAbsent(ctx, s.bucket, s.scopedKey(key), body, options)
	if err != nil {
		return storagepkg.ObjectInfo{}, err
	}
	info.Key, err = s.logicalKey(info.Key)
	if err != nil {
		return storagepkg.ObjectInfo{}, err
	}
	return info, nil
}

func (s *tenantStore) CopyIfAbsent(ctx context.Context, _ string, sourceKey string, _ string, destinationKey string) (storagepkg.CopyResult, error) {
	result, err := s.base.CopyIfAbsent(
		ctx,
		s.bucket,
		s.scopedKey(sourceKey),
		s.bucket,
		s.scopedKey(destinationKey),
	)
	if err != nil {
		return storagepkg.CopyResult{}, err
	}
	result.Key, err = s.logicalKey(result.Key)
	if err != nil {
		return storagepkg.CopyResult{}, err
	}
	return result, nil
}

func (s *tenantStore) BeginMultipart(ctx context.Context, _ string, key string, options storagepkg.MultipartOptions) (storagepkg.MultipartUpload, error) {
	upload, err := s.base.BeginMultipart(ctx, s.bucket, s.scopedKey(key), options)
	if err != nil {
		return storagepkg.MultipartUpload{}, err
	}
	logicalKey, err := s.logicalKey(upload.Key)
	if err != nil {
		return storagepkg.MultipartUpload{}, err
	}
	if upload.Bucket != s.bucket {
		return storagepkg.MultipartUpload{}, fmt.Errorf("%w: provider changed the multipart bucket", ErrTenantScopeViolation)
	}
	upload.Bucket = ""
	upload.Key = logicalKey
	return upload, nil
}

func (s *tenantStore) UploadPart(ctx context.Context, upload storagepkg.MultipartUpload, partNumber int32, body io.Reader, options storagepkg.PutOptions) (storagepkg.UploadedPart, error) {
	return s.base.UploadPart(ctx, s.physicalUpload(upload), partNumber, body, options)
}

func (s *tenantStore) CompleteMultipart(ctx context.Context, upload storagepkg.MultipartUpload, parts []storagepkg.UploadedPart) (storagepkg.ObjectInfo, error) {
	info, err := s.base.CompleteMultipart(ctx, s.physicalUpload(upload), parts)
	if err != nil {
		return storagepkg.ObjectInfo{}, err
	}
	info.Key, err = s.logicalKey(info.Key)
	if err != nil {
		return storagepkg.ObjectInfo{}, err
	}
	return info, nil
}

func (s *tenantStore) AbortMultipart(ctx context.Context, upload storagepkg.MultipartUpload) error {
	return s.base.AbortMultipart(ctx, s.physicalUpload(upload))
}

func (s *tenantStore) physicalUpload(upload storagepkg.MultipartUpload) storagepkg.MultipartUpload {
	upload.Bucket = s.bucket
	upload.Key = s.scopedKey(upload.Key)
	return upload
}

func (s *tenantStore) logicalKey(key string) (string, error) {
	boundary := s.prefix + "/"
	if !strings.HasPrefix(key, boundary) {
		return "", fmt.Errorf("%w: provider returned an out-of-scope object", ErrTenantScopeViolation)
	}
	return strings.TrimPrefix(key, boundary), nil
}

var _ storagepkg.ManagedStoreResolver = (*Resolver)(nil)
var _ storagepkg.BlobPlane = (*tenantStore)(nil)
