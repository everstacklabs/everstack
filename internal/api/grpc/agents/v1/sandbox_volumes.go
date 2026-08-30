package v1

// Persistent sandbox volumes (POR-77) — ConnectRPC.
//
// Volumes are named persistent storage units backed by S3-compatible object
// storage (R2) via FUSE mount. Multiple sandboxes can mount the same volume;
// subpath isolation (tenants/{tenant}/volumes/{id}/) prevents cross-volume
// contamination. Usage is measured hourly and billed per GiB-hour (no free
// allowance — the included GiB is root-disk only; see usage_meter.go).
//
// API (REST via grpc-gateway, ConnectRPC via agentsconnect):
//   GET    /v1/volumes          -- list volumes
//   POST   /v1/volumes          -- create volume
//   DELETE /v1/volumes/{id}     -- delete volume (+ purge object-store prefix)
//
// Attachment is specified at sandbox creation in the mounts field
// (type="everstack-volume", bucket=volume_id) — wired in a later slice.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
	sandboxcp "github.com/everstacklabs/everstack/internal/sandbox/controlplane"
	"github.com/everstacklabs/everstack/internal/sandbox/volstore"
	"github.com/everstacklabs/everstack/internal/storage"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
)

// Volume is a persistent storage unit.
type Volume struct {
	ID              string       `db:"id"                json:"id"`
	TenantID        string       `db:"tenant_id"         json:"tenant_id"`
	Name            string       `db:"name"              json:"name"`
	SizeBytes       int64        `db:"size_bytes"        json:"size_bytes"`
	UsedBytes       int64        `db:"used_bytes"        json:"used_bytes"`
	CreatedAt       time.Time    `db:"created_at"        json:"created_at"`
	UpdatedAt       time.Time    `db:"updated_at"        json:"updated_at"`
	UsageMeasuredAt sql.NullTime `db:"usage_measured_at" json:"usage_measured_at"`
}

// volumeObjectPrefix is the object-storage key prefix that backs a volume,
// within its tenant's bucket (volstore.BucketName(tenant)). Shared by the
// metering sweep, delete cleanup, and the attach mount rewrite so they all
// agree on where a volume's bytes live. The tenant is encoded in the bucket,
// so the prefix is just volumes/{id}/.
func volumeObjectPrefix(volumeID string) string {
	return sandboxcp.VolumeObjectPrefix(volumeID)
}

// volumeRepo handles volume DB operations.
type volumeRepo struct {
	db *sqlx.DB
}

func (r *volumeRepo) create(tenantID, name string, sizeBytes int64) (*Volume, error) {
	id := "vol_" + volRandomHex(10)
	const q = `
		INSERT INTO sandbox_volumes (id, tenant_id, name, size_bytes, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		RETURNING *`
	var v Volume
	if err := r.db.Get(&v, q, id, tenantID, name, sizeBytes); err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *volumeRepo) list(tenantID string) ([]Volume, error) {
	const q = `SELECT * FROM sandbox_volumes WHERE tenant_id = $1 ORDER BY updated_at DESC LIMIT 100`
	var vs []Volume
	if err := r.db.Select(&vs, q, tenantID); err != nil {
		return nil, err
	}
	if vs == nil {
		vs = []Volume{}
	}
	return vs, nil
}

// get fetches a single volume scoped to its tenant (nil if not found).
func (r *volumeRepo) get(tenantID, id string) (*Volume, error) {
	const q = `SELECT * FROM sandbox_volumes WHERE id = $1 AND tenant_id = $2`
	var v Volume
	if err := r.db.Get(&v, q, id, tenantID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &v, nil
}

// listAll returns every volume across tenants — used only by the metering
// sweep, which must measure all volumes regardless of caller.
func (r *volumeRepo) listAll() ([]Volume, error) {
	const q = `SELECT * FROM sandbox_volumes ORDER BY updated_at DESC`
	var vs []Volume
	if err := r.db.Select(&vs, q); err != nil {
		return nil, err
	}
	return vs, nil
}

// updateUsage records a fresh measurement of bytes stored for a volume.
func (r *volumeRepo) updateUsage(id string, usedBytes int64) error {
	const q = `UPDATE sandbox_volumes SET used_bytes = $2, usage_measured_at = NOW(), updated_at = NOW() WHERE id = $1`
	_, err := r.db.Exec(q, id, usedBytes)
	return err
}

func (r *volumeRepo) delete(tenantID, id string) error {
	const q = `DELETE FROM sandbox_volumes WHERE id = $1 AND tenant_id = $2`
	res, err := r.db.Exec(q, id, tenantID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

type volumeControlPlaneRepo struct {
	repo *volumeRepo
}

func (r volumeControlPlaneRepo) CreateVolume(_ context.Context, tenantID, name string, sizeBytes int64) (*sandboxcp.Volume, error) {
	vol, err := r.repo.create(tenantID, name, sizeBytes)
	if err != nil {
		return nil, err
	}
	return volumeToControlPlane(vol), nil
}

func (r volumeControlPlaneRepo) ListVolumes(_ context.Context, tenantID string) ([]sandboxcp.Volume, error) {
	vols, err := r.repo.list(tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]sandboxcp.Volume, len(vols))
	for i := range vols {
		out[i] = *volumeToControlPlane(&vols[i])
	}
	return out, nil
}

func (r volumeControlPlaneRepo) DeleteVolume(_ context.Context, tenantID, id string) error {
	if err := r.repo.delete(tenantID, id); err != nil {
		if err == sql.ErrNoRows {
			return sandboxcp.ErrVolumeNotFound
		}
		return err
	}
	return nil
}

func volumeToControlPlane(v *Volume) *sandboxcp.Volume {
	var measuredAt *time.Time
	if v.UsageMeasuredAt.Valid {
		measuredAt = &v.UsageMeasuredAt.Time
	}
	return &sandboxcp.Volume{
		ID:              v.ID,
		TenantID:        v.TenantID,
		Name:            v.Name,
		SizeBytes:       v.SizeBytes,
		UsedBytes:       v.UsedBytes,
		CreatedAt:       v.CreatedAt,
		UpdatedAt:       v.UpdatedAt,
		UsageMeasuredAt: measuredAt,
	}
}

func volRandomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func volumeToProto(v *Volume) *agentspb.SandboxVolume {
	measuredAt := ""
	if v.UsageMeasuredAt.Valid {
		measuredAt = v.UsageMeasuredAt.Time.Format(time.RFC3339)
	}
	return &agentspb.SandboxVolume{
		Id:              v.ID,
		TenantId:        v.TenantID,
		Name:            v.Name,
		SizeBytes:       v.SizeBytes,
		UsedBytes:       v.UsedBytes,
		CreatedAt:       v.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       v.UpdatedAt.Format(time.RFC3339),
		UsageMeasuredAt: measuredAt,
	}
}

func controlPlaneVolumeToProto(v *sandboxcp.Volume) *agentspb.SandboxVolume {
	measuredAt := ""
	if v.UsageMeasuredAt != nil {
		measuredAt = v.UsageMeasuredAt.Format(time.RFC3339)
	}
	return &agentspb.SandboxVolume{
		Id:              v.ID,
		TenantId:        v.TenantID,
		Name:            v.Name,
		SizeBytes:       v.SizeBytes,
		UsedBytes:       v.UsedBytes,
		CreatedAt:       v.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       v.UpdatedAt.Format(time.RFC3339),
		UsageMeasuredAt: measuredAt,
	}
}

// ListSandboxVolumes implements AgentsServiceHandler.ListSandboxVolumes.
func (s *Server) ListSandboxVolumes(
	ctx context.Context,
	_ *connect.Request[agentspb.ListSandboxVolumesRequest],
) (*connect.Response[agentspb.ListSandboxVolumesResponse], error) {
	if s.db == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("database not configured"))
	}
	scope, err := s.resolveSandboxTenantInstanceScope(ctx, "")
	if err != nil {
		return nil, err
	}
	vols, err := sandboxcp.NewVolumeService(volumeControlPlaneRepo{repo: &volumeRepo{db: s.db}}, s.volumeStore).ListVolumes(ctx, scope)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*agentspb.SandboxVolume, len(vols))
	for i := range vols {
		out[i] = controlPlaneVolumeToProto(&vols[i])
	}
	return connect.NewResponse(&agentspb.ListSandboxVolumesResponse{
		Volumes: out,
		Total:   int32(len(out)),
	}), nil
}

// CreateSandboxVolume implements AgentsServiceHandler.CreateSandboxVolume.
func (s *Server) CreateSandboxVolume(
	ctx context.Context,
	req *connect.Request[agentspb.CreateSandboxVolumeRequest],
) (*connect.Response[agentspb.CreateSandboxVolumeResponse], error) {
	if s.db == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("database not configured"))
	}
	scope, err := s.resolveSandboxTenantInstanceScope(ctx, "")
	if err != nil {
		return nil, err
	}
	vol, err := sandboxcp.NewVolumeService(volumeControlPlaneRepo{repo: &volumeRepo{db: s.db}}, s.volumeStore).CreateVolume(ctx, sandboxcp.CreateVolumeRequest{
		Scope:  scope,
		Name:   req.Msg.GetName(),
		SizeGB: req.Msg.GetSizeGb(),
	})
	if err != nil {
		if err == sandboxcp.ErrVolumeNameRequired {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&agentspb.CreateSandboxVolumeResponse{
		Volume: controlPlaneVolumeToProto(vol),
	}), nil
}

// DeleteSandboxVolume implements AgentsServiceHandler.DeleteSandboxVolume.
// Purges the volume's object-storage prefix before dropping the DB row so we
// don't orphan stored bytes (or keep billing for them).
func (s *Server) DeleteSandboxVolume(
	ctx context.Context,
	req *connect.Request[agentspb.DeleteSandboxVolumeRequest],
) (*connect.Response[agentspb.DeleteSandboxVolumeResponse], error) {
	if s.db == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("database not configured"))
	}
	scope, err := s.resolveSandboxTenantInstanceScope(ctx, "")
	if err != nil {
		return nil, err
	}
	if err := sandboxcp.NewVolumeService(volumeControlPlaneRepo{repo: &volumeRepo{db: s.db}}, s.volumeStore).DeleteVolume(ctx, scope, req.Msg.GetVolumeId()); err != nil {
		if err == sandboxcp.ErrVolumeIDRequired {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		if err == sandboxcp.ErrVolumeNotFound {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("volume not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&agentspb.DeleteSandboxVolumeResponse{}), nil
}

// SetVolumeStore wires the object store + bucket that back persistent volumes.
// Reuses the sandbox R2 store. nil/empty disables volume backing (metadata
// only) — create/list/delete still work; measurement and purge become no-ops.
func (s *Server) SetVolumeStore(store storage.ObjectStore, bucket string) {
	s.volumeStore = store
	s.volumeBucket = bucket
}

// VolumeStore exposes the configured volume object store + bucket for the
// metering sweep. Returns (nil, "") when volume backing is not configured.
func (s *Server) VolumeStore() (storage.ObjectStore, string) {
	return s.volumeStore, s.volumeBucket
}

// SetVolumeProvisioner wires the per-tenant R2 bucket/token provisioner used to
// resolve everstack-volume mounts. nil disables volume attach (metadata only).
func (s *Server) SetVolumeProvisioner(p *volstore.Provisioner) {
	s.volumeProvisioner = p
}

// resolveVolumeMount turns an "everstack-volume" mount (bucket = volume_id) into
// a concrete, credentialed r2 mount. It enforces tenant ISOLATION: the volume
// is looked up scoped to the sandbox's tenant, so a volume owned by a different
// tenant simply isn't found and the mount is dropped. Returns ok=false (mount
// skipped, logged) on any failure — a missing mount must never silently become
// access to the wrong data. The credential is a long-lived, bucket-scoped R2
// token for the tenant's own bucket.
func (s *Server) resolveVolumeMount(ctx context.Context, tenantID, volumeID, mountPath string, readOnly bool) (sandbox.StorageMountConfig, bool) {
	if s.db == nil {
		return sandbox.StorageMountConfig{}, false
	}
	cleanMountPath, pathErr := sandboxcp.NormalizeVolumeMountPath(mountPath)
	if pathErr != nil {
		logger.WithFields("volume_id", volumeID, "mount_path", mountPath, "error", pathErr.Error()).
			Warn("everstack-volume: invalid mount path; skipping mount")
		return sandbox.StorageMountConfig{}, false
	}
	if s.volumeProvisioner == nil {
		logger.WithFields("volume_id", volumeID).
			Warn("everstack-volume: volume storage not configured; skipping mount")
		return sandbox.StorageMountConfig{}, false
	}
	vol, err := (&volumeRepo{db: s.db}).get(tenantID, volumeID)
	if err != nil || vol == nil {
		logger.WithFields("volume_id", volumeID, "tenant_id", tenantID).
			Warn("everstack-volume: not found or not owned by tenant; skipping mount")
		return sandbox.StorageMountConfig{}, false
	}
	tb, err := s.volumeProvisioner.Resolve(ctx, tenantID)
	if err != nil {
		logger.WithFields("volume_id", volumeID, "tenant_id", tenantID, "error", err.Error()).
			Warn("everstack-volume: bucket provisioning failed; skipping mount")
		return sandbox.StorageMountConfig{}, false
	}
	return sandbox.StorageMountConfig{
		Type:            "r2",
		Bucket:          tb.BucketName,
		MountPath:       cleanMountPath,
		Endpoint:        tb.Endpoint,
		SubPath:         sandboxcp.VolumeObjectSubPath(volumeID),
		ReadOnly:        readOnly,
		AccessKeyID:     tb.AccessKeyID,
		SecretAccessKey: tb.SecretAccessKey,
	}, true
}
