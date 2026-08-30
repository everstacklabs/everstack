package controlplane

import (
	"context"
	"errors"
	"fmt"
	pathpkg "path"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/volstore"
	"github.com/everstacklabs/everstack/internal/storage"
)

var (
	ErrVolumesNotConfigured = errors.New("database not configured")
	ErrVolumeNameRequired   = errors.New("name is required")
	ErrVolumeIDRequired     = errors.New("volume_id is required")
	ErrVolumeNotFound       = errors.New("volume not found")
	ErrVolumeScopeRequired  = errors.New("volume tenant scope is required")
	ErrVolumeMountPath      = errors.New("volume mount_path is invalid")
	ErrVolumeSubPath        = errors.New("volume subpath is invalid")
)

type Volume struct {
	ID              string
	TenantID        string
	Name            string
	SizeBytes       int64
	UsedBytes       int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
	UsageMeasuredAt *time.Time
}

type VolumeRepository interface {
	CreateVolume(ctx context.Context, tenantID, name string, sizeBytes int64) (*Volume, error)
	ListVolumes(ctx context.Context, tenantID string) ([]Volume, error)
	DeleteVolume(ctx context.Context, tenantID, id string) error
}

type VolumeService struct {
	repo  VolumeRepository
	store storage.ObjectStore
}

type CreateVolumeRequest struct {
	Scope  sandbox.TenantInstanceScope
	Name   string
	SizeGB int64
}

func NewVolumeService(repo VolumeRepository, store storage.ObjectStore) *VolumeService {
	return &VolumeService{repo: repo, store: store}
}

func (s *VolumeService) Configured() bool {
	return s != nil && s.repo != nil
}

func (s *VolumeService) ListVolumes(ctx context.Context, scope sandbox.TenantInstanceScope) ([]Volume, error) {
	if !s.Configured() {
		return nil, ErrVolumesNotConfigured
	}
	tenantID, err := volumeTenantID(scope)
	if err != nil {
		return nil, err
	}
	return s.repo.ListVolumes(ctx, tenantID)
}

func (s *VolumeService) CreateVolume(ctx context.Context, req CreateVolumeRequest) (*Volume, error) {
	if !s.Configured() {
		return nil, ErrVolumesNotConfigured
	}
	tenantID, err := volumeTenantID(req.Scope)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, ErrVolumeNameRequired
	}
	sizeBytes := req.SizeGB * 1024 * 1024 * 1024
	if sizeBytes < 0 {
		sizeBytes = 0
	}
	return s.repo.CreateVolume(ctx, tenantID, name, sizeBytes)
}

func (s *VolumeService) DeleteVolume(ctx context.Context, scope sandbox.TenantInstanceScope, id string) error {
	if !s.Configured() {
		return ErrVolumesNotConfigured
	}
	tenantID, err := volumeTenantID(scope)
	if err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" {
		return ErrVolumeIDRequired
	}
	s.purgeVolumeObjects(ctx, tenantID, id)
	return s.repo.DeleteVolume(ctx, tenantID, id)
}

func (s *VolumeService) purgeVolumeObjects(ctx context.Context, tenantID, volumeID string) {
	if s.store == nil {
		return
	}
	bucket := volstore.BucketName(tenantID)
	prefix := VolumeObjectPrefix(volumeID)
	objs, err := s.store.List(ctx, bucket, prefix)
	if err != nil {
		logger.WithFields("volume_id", volumeID, "error", err.Error()).
			Warn("volume_delete: failed to list objects for purge")
		return
	}
	for _, o := range objs {
		if derr := s.store.Delete(ctx, bucket, o.Key); derr != nil {
			logger.WithFields("volume_id", volumeID, "key", o.Key, "error", derr.Error()).
				Warn("volume_delete: failed to delete object")
		}
	}
}

func VolumeObjectPrefix(volumeID string) string {
	return fmt.Sprintf("volumes/%s/", volumeID)
}

func VolumeObjectSubPath(volumeID string) string {
	return "volumes/" + strings.Trim(strings.TrimSpace(volumeID), "/")
}

func NormalizeVolumeMountPath(mountPath string) (string, error) {
	raw := strings.TrimSpace(mountPath)
	if raw == "" {
		return "", fmt.Errorf("%w: path is required", ErrVolumeMountPath)
	}
	if strings.ContainsRune(raw, '\x00') || !strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("%w: path must be absolute", ErrVolumeMountPath)
	}
	cleaned := pathpkg.Clean(raw)
	if cleaned == "/" {
		return "", fmt.Errorf("%w: path must not be root", ErrVolumeMountPath)
	}
	if !isAllowedVolumeMountRoot(cleaned) {
		return "", fmt.Errorf("%w: path must be under /mnt or /workspace/mounts", ErrVolumeMountPath)
	}
	return cleaned, nil
}

func NormalizeVolumeSubPath(subPath string) (string, error) {
	original := strings.TrimSpace(subPath)
	if strings.ContainsRune(original, '\x00') || strings.HasPrefix(original, "/") {
		return "", fmt.Errorf("%w: path must be relative", ErrVolumeSubPath)
	}
	raw := strings.Trim(original, "/")
	if raw == "" {
		return "", nil
	}
	cleaned := pathpkg.Clean(raw)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return "", fmt.Errorf("%w: path must not traverse upward", ErrVolumeSubPath)
	}
	return cleaned, nil
}

func isAllowedVolumeMountRoot(cleaned string) bool {
	return cleaned == "/mnt" || strings.HasPrefix(cleaned, "/mnt/") ||
		cleaned == "/workspace/mounts" || strings.HasPrefix(cleaned, "/workspace/mounts/")
}

func volumeTenantID(scope sandbox.TenantInstanceScope) (string, error) {
	scope = scope.Normalize()
	if scope.TenantID != "" {
		return scope.TenantID, nil
	}
	if scope.OrganizationID != "" {
		return scope.OrganizationID, nil
	}
	return "", fmt.Errorf("%w", ErrVolumeScopeRequired)
}
