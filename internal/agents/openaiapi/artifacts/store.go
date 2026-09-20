package artifacts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/agents/openaiapi/executor"
	"github.com/everstacklabs/everstack/internal/enterprise"
	"github.com/everstacklabs/everstack/internal/storage"
	"github.com/everstacklabs/everstack/internal/storageauth"
)

const referenceType = "openai_agents_session"

type UploadWriter interface {
	Upload(context.Context, storage.InitiateUploadParams, io.Reader) (*storage.Upload, string, error)
}
type Lifecycle interface {
	BeginDeletion(context.Context, string, string, time.Time) (*storage.Upload, bool, error)
	FailDeletion(context.Context, string, string, string, time.Time, time.Time) (*storage.Upload, error)
	CompleteDeletion(context.Context, string, string, time.Time) (*storage.Upload, bool, error)
}
type Scope struct{ OrganizationID, InstanceID, SessionID, SandboxID string }
type Backend struct {
	Objects          storage.ObjectStore
	Uploader         UploadWriter
	Bucket, ConfigID string
}
type Resolver interface {
	Resolve(context.Context, Scope) (Backend, error)
}
type ResolverFunc func(context.Context, Scope) (Backend, error)

func (f ResolverFunc) Resolve(ctx context.Context, s Scope) (Backend, error) { return f(ctx, s) }

type RetainedArtifact struct {
	ObjectID, Filename, ContentType string
	Kind                            string
	SizeBytes                       int64
	SHA256                          string
	CreatedAt                       time.Time
}
type Download struct {
	RetainedArtifact
	URL       string
	ExpiresAt time.Time
}
type Store struct {
	db        *sqlx.DB
	lifecycle Lifecycle
	resolver  Resolver
	quota     func(context.Context) int64
}

func NewStore(db *sqlx.DB, lifecycle Lifecycle, resolver Resolver) *Store {
	return &Store{db: db, lifecycle: lifecycle, resolver: resolver, quota: entitlementQuota}
}
func NewFixedStore(db *sqlx.DB, objects storage.ObjectStore, uploader UploadWriter, lifecycle Lifecycle, bucket, configID string) *Store {
	return NewStore(db, lifecycle, ResolverFunc(func(context.Context, Scope) (Backend, error) {
		return Backend{Objects: objects, Uploader: uploader, Bucket: bucket, ConfigID: configID}, nil
	}))
}

func (s *Store) UploadInput(ctx context.Context, scope Scope, filename, contentType string, data []byte) (*RetainedArtifact, error) {
	if err := authorize(ctx, storageauth.ActionUploadInternal, scope); err != nil {
		return nil, err
	}
	return s.upload(ctx, scope, "input", filename, contentType, data)
}
func (s *Store) ResolveInput(ctx context.Context, req executor.InputRequest) (*executor.InputFile, error) {
	scope := Scope{req.OrganizationID, req.InstanceID, req.LocalSessionID, req.SandboxID}
	if err := authorize(ctx, storageauth.ActionObjectDownload, scope); err != nil {
		return nil, err
	}
	backend, err := s.resolve(ctx, scope)
	if err != nil {
		return nil, err
	}
	obj, err := s.lookup(ctx, scope, backend.ConfigID, req.ReferenceID, "input", false)
	if err != nil {
		return nil, err
	}
	blob, err := storage.RequireBlobPlane(backend.Objects)
	if err != nil {
		return nil, err
	}
	reader, err := blob.Get(ctx, backend.Bucket, obj.Key)
	if err != nil {
		return nil, fmt.Errorf("read input object: %w", err)
	}
	if reader == nil || reader.Body == nil {
		return nil, errors.New("storage provider returned an invalid input")
	}
	defer reader.Body.Close()
	data, err := io.ReadAll(io.LimitReader(reader.Body, obj.SizeBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != obj.SizeBytes || digest(data) != obj.SHA256 {
		return nil, errors.New("input size or checksum mismatch")
	}
	return &executor.InputFile{ReferenceID: obj.ObjectID, Filename: obj.Filename, Content: data, SHA256: obj.SHA256}, nil
}
func (s *Store) Export(ctx context.Context, req executor.ExportRequest) (*executor.Artifact, error) {
	scope := Scope{req.OrganizationID, req.InstanceID, req.LocalSessionID, req.SandboxID}
	if err := authorize(ctx, storageauth.ActionArtifactPromote, scope); err != nil {
		return nil, err
	}
	if req.SHA256 == "" || digest(req.Content) != req.SHA256 {
		return nil, errors.New("sandbox artifact checksum mismatch")
	}
	a, err := s.upload(ctx, scope, "artifact", req.Filename, "", req.Content)
	if err != nil {
		return nil, err
	}
	return &executor.Artifact{ObjectID: a.ObjectID, Filename: a.Filename, SizeBytes: a.SizeBytes, SHA256: a.SHA256}, nil
}
func (s *Store) List(ctx context.Context, scope Scope) ([]RetainedArtifact, error) {
	if err := authorize(ctx, storageauth.ActionObjectList, scope); err != nil {
		return nil, err
	}
	backend, err := s.resolve(ctx, scope)
	if err != nil {
		return nil, err
	}
	if s.db == nil {
		return nil, errors.New("storage database is not configured")
	}
	var rows []objectRow
	err = s.db.SelectContext(ctx, &rows, `SELECT id, key, filename, content_type, size_bytes, checksum_sha256, COALESCE(metadata->>'openai_file_kind', purpose) AS purpose, created_at
 FROM object_storage_objects WHERE tenant_id = $1 AND config_id = $2 AND reference_type = $3 AND reference_id = $4
 AND metadata->>'openai_instance_id' = $5 AND metadata->>'openai_sandbox_id' = $6 AND deleted_at IS NULL ORDER BY created_at DESC`,
		scope.OrganizationID, backend.ConfigID, referenceType, scope.SessionID, scope.InstanceID, scope.SandboxID)
	if err != nil {
		return nil, fmt.Errorf("list OpenAI session artifacts: %w", err)
	}
	out := make([]RetainedArtifact, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.artifact())
	}
	return out, nil
}
func (s *Store) Download(ctx context.Context, scope Scope, objectID string, expiry time.Duration) (*Download, error) {
	if err := authorize(ctx, storageauth.ActionObjectDownload, scope); err != nil {
		return nil, err
	}
	backend, err := s.resolve(ctx, scope)
	if err != nil {
		return nil, err
	}
	obj, err := s.lookup(ctx, scope, backend.ConfigID, objectID, "", false)
	if err != nil {
		return nil, err
	}
	if expiry <= 0 || expiry > 15*time.Minute {
		expiry = 15 * time.Minute
	}
	u, err := backend.Objects.GetPresignedURL(ctx, backend.Bucket, obj.Key, expiry)
	if err != nil {
		return nil, err
	}
	return &Download{RetainedArtifact: obj.artifact(), URL: u, ExpiresAt: time.Now().UTC().Add(expiry)}, nil
}
func (s *Store) Delete(ctx context.Context, scope Scope, objectID string) error {
	if err := authorize(ctx, storageauth.ActionObjectDelete, scope); err != nil {
		return err
	}
	backend, err := s.resolve(ctx, scope)
	if err != nil {
		return err
	}
	if _, err = s.lookup(ctx, scope, backend.ConfigID, objectID, "", true); err != nil {
		return err
	}
	if s.lifecycle == nil {
		return errors.New("storage deletion lifecycle is not configured")
	}
	now := time.Now().UTC()
	upload, deleted, err := s.lifecycle.BeginDeletion(ctx, scope.OrganizationID, objectID, now)
	if err != nil {
		return err
	}
	if deleted {
		return nil
	}
	if err = backend.Objects.Delete(ctx, backend.Bucket, upload.Key); err != nil && !storage.IsCode(err, storage.ErrorNotFound) {
		_, _ = s.lifecycle.FailDeletion(ctx, scope.OrganizationID, objectID, "provider_delete_failed", now.Add(time.Minute), now)
		return fmt.Errorf("delete artifact provider bytes: %w", err)
	}
	_, _, err = s.lifecycle.CompleteDeletion(ctx, scope.OrganizationID, objectID, time.Now().UTC())
	return err
}

// DeleteAll removes every retained input and exported artifact owned by the
// authenticated session. It continues after individual failures so a retry
// has the smallest possible remainder; lifecycle deletion is idempotent and
// remains the single place that releases storage accounting.
func (s *Store) DeleteAll(ctx context.Context, scope Scope) error {
	artifacts, err := s.List(ctx, scope)
	if err != nil {
		return err
	}
	var cleanupErrors []error
	for _, artifact := range artifacts {
		if err := s.Delete(ctx, scope, artifact.ObjectID); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("delete retained object %s: %w", artifact.ObjectID, err))
		}
	}
	return errors.Join(cleanupErrors...)
}

func (s *Store) upload(ctx context.Context, scope Scope, purpose, filename, contentType string, data []byte) (*RetainedArtifact, error) {
	backend, err := s.resolve(ctx, scope)
	if err != nil {
		return nil, err
	}
	if backend.Uploader == nil {
		return nil, errors.New("verified storage uploader is not configured")
	}
	filename = filepath.Base(strings.TrimSpace(filename))
	if filename == "" || filename == "." {
		return nil, errors.New("artifact filename is required")
	}
	if contentType == "" {
		contentType = mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	id, checksum := uuid.NewString(), digest(data)
	metadata, _ := json.Marshal(map[string]string{"openai_instance_id": scope.InstanceID, "openai_session_id": scope.SessionID, "openai_sandbox_id": scope.SandboxID, "openai_file_kind": purpose})
	fp := sha256.Sum256([]byte(strings.Join([]string{"openai-agent-artifact:v1", scope.OrganizationID, scope.InstanceID, scope.SessionID, scope.SandboxID, purpose, filename, contentType, checksum}, "\x00")))
	now := time.Now().UTC()
	storagePurpose := purpose
	if purpose == "input" {
		storagePurpose = "upload"
	}
	upload, _, err := backend.Uploader.Upload(ctx, storage.InitiateUploadParams{
		ObjectID: id, TenantID: scope.OrganizationID, ConfigID: backend.ConfigID,
		Key:      fmt.Sprintf("tenants/%s/openai-agents/%s/%s/%s", scope.OrganizationID, scope.SessionID, id, filename),
		Filename: filename, ContentType: contentType, ExpectedSizeBytes: int64(len(data)), ExpectedChecksumSHA256: checksum,
		Purpose: storagePurpose, ReferenceID: scope.SessionID, ReferenceType: referenceType, Metadata: metadata,
		IdempotencyKey: "openai-agent-artifact:" + id, RequestFingerprint: hex.EncodeToString(fp[:]), QuotaBytes: s.quota(ctx), ExpiresAt: now.Add(15 * time.Minute), Now: now,
	}, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("persist OpenAI session %s: %w", purpose, err)
	}
	return &RetainedArtifact{ObjectID: upload.ObjectID, Filename: upload.Filename, ContentType: upload.ContentType, Kind: purpose, SizeBytes: upload.ActualSizeBytes, SHA256: upload.ActualChecksumSHA256, CreatedAt: upload.CreatedAt}, nil
}

type objectRow struct {
	ObjectID    string    `db:"id"`
	Key         string    `db:"key"`
	Filename    string    `db:"filename"`
	ContentType string    `db:"content_type"`
	SizeBytes   int64     `db:"size_bytes"`
	SHA256      string    `db:"checksum_sha256"`
	Purpose     string    `db:"purpose"`
	CreatedAt   time.Time `db:"created_at"`
}

func (r objectRow) artifact() RetainedArtifact {
	return RetainedArtifact{ObjectID: r.ObjectID, Filename: r.Filename, ContentType: r.ContentType, Kind: r.Purpose, SizeBytes: r.SizeBytes, SHA256: r.SHA256, CreatedAt: r.CreatedAt}
}
func (s *Store) lookup(ctx context.Context, scope Scope, configID, objectID, purpose string, includeDeleted bool) (*objectRow, error) {
	if s.db == nil {
		return nil, errors.New("storage database is not configured")
	}
	q := `SELECT id, key, filename, content_type, size_bytes, checksum_sha256, COALESCE(metadata->>'openai_file_kind', purpose) AS purpose, created_at FROM object_storage_objects
 WHERE id = $1 AND tenant_id = $2 AND config_id = $3 AND reference_type = $4 AND reference_id = $5
 AND metadata->>'openai_instance_id' = $6 AND metadata->>'openai_sandbox_id' = $7`
	args := []any{objectID, scope.OrganizationID, configID, referenceType, scope.SessionID, scope.InstanceID, scope.SandboxID}
	if !includeDeleted {
		q += " AND deleted_at IS NULL"
	}
	if purpose != "" {
		q += " AND metadata->>'openai_file_kind' = $8"
		args = append(args, purpose)
	}
	var row objectRow
	if err := s.db.GetContext(ctx, &row, q, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("artifact not found in authenticated session")
		}
		return nil, err
	}
	return &row, nil
}
func (s *Store) resolve(ctx context.Context, scope Scope) (Backend, error) {
	if s == nil || s.resolver == nil {
		return Backend{}, errors.New("artifact storage resolver is not configured")
	}
	b, err := s.resolver.Resolve(ctx, scope)
	if err != nil {
		return Backend{}, fmt.Errorf("resolve artifact storage: %w", err)
	}
	if b.Objects == nil || strings.TrimSpace(b.ConfigID) == "" {
		return Backend{}, errors.New("artifact storage resolver returned an incomplete backend")
	}
	return b, nil
}
func authorize(ctx context.Context, action storageauth.Action, s Scope) error {
	if strings.TrimSpace(s.OrganizationID) == "" || strings.TrimSpace(s.InstanceID) == "" || strings.TrimSpace(s.SessionID) == "" || strings.TrimSpace(s.SandboxID) == "" {
		return errors.New("complete authenticated artifact scope is required")
	}
	_, err := storageauth.AuthorizeTenant(ctx, action, s.OrganizationID)
	return err
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func entitlementQuota(ctx context.Context) int64 {
	if n, ok := enterprise.ResolveEntitlements(ctx, enterprise.LicenseMonitorFromContext(ctx)).Limit(enterprise.UsageTypeStorageBytes); ok {
		return n
	}
	return -1
}

var _ executor.ArtifactBridge = (*Store)(nil)
