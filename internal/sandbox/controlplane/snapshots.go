package controlplane

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/snapshots"
)

var (
	ErrSnapshotsNotConfigured = errors.New("snapshots not configured")
	ErrSnapshotNameRequired   = errors.New("name is required")
	ErrSnapshotImageRequired  = errors.New("image or from_sandbox_id is required")
	ErrSnapshotScopeRequired  = errors.New("snapshot tenant scope is required")
)

type SnapshotRepository interface {
	Create(ctx context.Context, p snapshots.CreateParams) (*snapshots.Snapshot, error)
	GetByID(ctx context.Context, tenantID, id string) (*snapshots.Snapshot, error)
	ListByTenant(ctx context.Context, tenantID string) ([]snapshots.Snapshot, error)
	Delete(ctx context.Context, tenantID, id string) error
}

type SnapshotService struct {
	repo SnapshotRepository
}

type CreateSnapshotRequest struct {
	Scope         sandbox.TenantInstanceScope
	Name          string
	Image         string
	FromSandboxID string
}

func NewSnapshotService(repo SnapshotRepository) *SnapshotService {
	return &SnapshotService{repo: repo}
}

func (s *SnapshotService) Configured() bool {
	return s != nil && s.repo != nil
}

func (s *SnapshotService) ListSnapshots(ctx context.Context, scope sandbox.TenantInstanceScope) ([]snapshots.Snapshot, error) {
	if !s.Configured() {
		return nil, ErrSnapshotsNotConfigured
	}
	tenantID, err := snapshotTenantID(scope)
	if err != nil {
		return nil, err
	}
	return s.repo.ListByTenant(ctx, tenantID)
}

func (s *SnapshotService) CreateSnapshot(ctx context.Context, req CreateSnapshotRequest) (*snapshots.Snapshot, error) {
	if !s.Configured() {
		return nil, ErrSnapshotsNotConfigured
	}
	tenantID, err := snapshotTenantID(req.Scope)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, ErrSnapshotNameRequired
	}
	if strings.TrimSpace(req.Image) == "" && strings.TrimSpace(req.FromSandboxID) == "" {
		return nil, ErrSnapshotImageRequired
	}
	return s.repo.Create(ctx, snapshots.CreateParams{
		TenantID:      tenantID,
		Name:          name,
		Image:         req.Image,
		FromSandboxID: req.FromSandboxID,
	})
}

func (s *SnapshotService) GetSnapshot(ctx context.Context, scope sandbox.TenantInstanceScope, id string) (*snapshots.Snapshot, error) {
	if !s.Configured() {
		return nil, ErrSnapshotsNotConfigured
	}
	tenantID, err := snapshotTenantID(scope)
	if err != nil {
		return nil, err
	}
	return s.repo.GetByID(ctx, tenantID, id)
}

func (s *SnapshotService) DeleteSnapshot(ctx context.Context, scope sandbox.TenantInstanceScope, id string) error {
	if !s.Configured() {
		return ErrSnapshotsNotConfigured
	}
	tenantID, err := snapshotTenantID(scope)
	if err != nil {
		return err
	}
	return s.repo.Delete(ctx, tenantID, id)
}

func snapshotTenantID(scope sandbox.TenantInstanceScope) (string, error) {
	scope = scope.Normalize()
	if scope.TenantID != "" {
		return scope.TenantID, nil
	}
	if scope.OrganizationID != "" {
		return scope.OrganizationID, nil
	}
	return "", fmt.Errorf("%w", ErrSnapshotScopeRequired)
}
