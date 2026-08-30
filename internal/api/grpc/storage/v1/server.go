package v1

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/enterprise"
	"github.com/everstacklabs/everstack/internal/storage"
	storagecredentials "github.com/everstacklabs/everstack/internal/storage/credentials"
	s3store "github.com/everstacklabs/everstack/internal/storage/s3"
	"github.com/everstacklabs/everstack/internal/storageauth"
	storagepb "github.com/everstacklabs/everstack/pkg/grpc/everstack/storage/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/storage/v1/storageconnect"
	"github.com/google/uuid"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/jmoiron/sqlx"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var _ storagepb.ObjectStorageServiceServer = (*GrpcServer)(nil)

var _ interface {
	RegisterConnectServer(...connect.Interceptor) (string, http.Handler)
	FileDescriptor() protoreflect.FileDescriptor
} = (*Server)(nil)

type Server struct {
	ctx                context.Context
	store              storage.ObjectStore
	db                 *sqlx.DB
	credentialStore    storagecredentials.Store
	connectionVerifier storage.ConnectionVerifier
	managedDefaults    storage.ManagedDefaultEnsurer
	managedResolver    storage.ManagedStoreResolver
	uploadLifecycle    *storage.PostgresUploadLifecycle
}

// SetManagedStorage enables the system-managed Everstack Storage path. The
// default ensurer owns logical tenant records; the resolver owns physical cell
// configuration and platform credentials.
func (s *Server) SetManagedStorage(defaults storage.ManagedDefaultEnsurer, resolver storage.ManagedStoreResolver) {
	s.managedDefaults = defaults
	s.managedResolver = resolver
}

type GrpcServer struct {
	storagepb.UnimplementedObjectStorageServiceServer
	base *Server
}

func CreateServer() *Server {
	return &Server{connectionVerifier: s3ConnectionVerifier{}}
}

func CreateServerWithContext(ctx context.Context) *Server {
	return &Server{ctx: ctx, connectionVerifier: s3ConnectionVerifier{}}
}

func CreateServerWithDeps(ctx context.Context, store storage.ObjectStore, db *sqlx.DB) *Server {
	var credentialStore storagecredentials.Store
	if db != nil {
		configured, err := storagecredentials.NewConfiguredPostgresStore(db)
		if err != nil {
			slog.Warn("storage credential backend unavailable", "error", err.Error())
		} else {
			credentialStore = configured
		}
	}
	return CreateServerWithSecurityDeps(ctx, store, db, credentialStore, s3ConnectionVerifier{})
}

func CreateServerWithSecurityDeps(ctx context.Context, store storage.ObjectStore, db *sqlx.DB, credentialStore storagecredentials.Store, verifier storage.ConnectionVerifier) *Server {
	if verifier == nil {
		verifier = s3ConnectionVerifier{}
	}
	return &Server{
		ctx:                ctx,
		store:              store,
		db:                 db,
		credentialStore:    credentialStore,
		connectionVerifier: verifier,
		uploadLifecycle:    storage.NewPostgresUploadLifecycle(db),
	}
}

type s3ConnectionVerifier struct{}

func (s3ConnectionVerifier) Verify(ctx context.Context, cfg storage.ConnectionConfig, credentials storagecredentials.ProviderCredentials) error {
	store, err := s3store.New(ctx, s3store.Config{
		Endpoint: cfg.Endpoint, Region: cfg.Region, Bucket: cfg.Bucket,
		AccessKeyID: credentials.AccessKeyID, SecretAccessKey: credentials.SecretAccessKey,
		PathPrefix: cfg.PathPrefix, ForcePathStyle: cfg.ForcePathStyle,
		EnforceManagedEgress: enterprise.ManagedGateway(),
	})
	if err != nil {
		return err
	}
	return store.Verify(ctx, cfg.Bucket)
}

func CreateClassicServer() storagepb.ObjectStorageServiceServer {
	return &GrpcServer{base: CreateServer()}
}

func CreateClassicServerWithContext(ctx context.Context) storagepb.ObjectStorageServiceServer {
	return &GrpcServer{base: CreateServerWithContext(ctx)}
}

func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	return storageconnect.NewObjectStorageServiceHandler(s, connect.WithInterceptors(interceptors...))
}

func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return storagepb.File_everstack_storage_v1_storage_service_proto
}

func (s *Server) AppName() string {
	return storageconnect.ObjectStorageServiceName
}

func (s *Server) MethodPrefix() string {
	return storageconnect.ObjectStorageServiceName
}

func (s *Server) RegisterGateway(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) error {
	return storagepb.RegisterObjectStorageServiceHandlerFromEndpoint(ctx, mux, endpoint, opts)
}

// GrpcServer wrapper methods
func (g *GrpcServer) ConfigureStorage(ctx context.Context, req *storagepb.ConfigureStorageRequest) (*storagepb.ConfigureStorageResponse, error) {
	cReq := &connect.Request[storagepb.ConfigureStorageRequest]{Msg: req}
	resp, err := g.base.ConfigureStorage(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetStorageConfig(ctx context.Context, req *storagepb.GetStorageConfigRequest) (*storagepb.GetStorageConfigResponse, error) {
	cReq := &connect.Request[storagepb.GetStorageConfigRequest]{Msg: req}
	resp, err := g.base.GetStorageConfig(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListStorageConfigs(ctx context.Context, req *storagepb.ListStorageConfigsRequest) (*storagepb.ListStorageConfigsResponse, error) {
	cReq := &connect.Request[storagepb.ListStorageConfigsRequest]{Msg: req}
	resp, err := g.base.ListStorageConfigs(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) UpdateStorageConfig(ctx context.Context, req *storagepb.UpdateStorageConfigRequest) (*storagepb.UpdateStorageConfigResponse, error) {
	cReq := &connect.Request[storagepb.UpdateStorageConfigRequest]{Msg: req}
	resp, err := g.base.UpdateStorageConfig(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) DeleteStorageConfig(ctx context.Context, req *storagepb.DeleteStorageConfigRequest) (*storagepb.DeleteStorageConfigResponse, error) {
	cReq := &connect.Request[storagepb.DeleteStorageConfigRequest]{Msg: req}
	resp, err := g.base.DeleteStorageConfig(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetPresignedUploadURL(ctx context.Context, req *storagepb.GetPresignedUploadURLRequest) (*storagepb.GetPresignedUploadURLResponse, error) {
	cReq := &connect.Request[storagepb.GetPresignedUploadURLRequest]{Msg: req}
	resp, err := g.base.GetPresignedUploadURL(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) CompleteUpload(ctx context.Context, req *storagepb.CompleteUploadRequest) (*storagepb.CompleteUploadResponse, error) {
	cReq := &connect.Request[storagepb.CompleteUploadRequest]{Msg: req}
	resp, err := g.base.CompleteUpload(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetUploadStatus(ctx context.Context, req *storagepb.GetUploadStatusRequest) (*storagepb.GetUploadStatusResponse, error) {
	cReq := &connect.Request[storagepb.GetUploadStatusRequest]{Msg: req}
	resp, err := g.base.GetUploadStatus(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetPresignedDownloadURL(ctx context.Context, req *storagepb.GetPresignedDownloadURLRequest) (*storagepb.GetPresignedDownloadURLResponse, error) {
	cReq := &connect.Request[storagepb.GetPresignedDownloadURLRequest]{Msg: req}
	resp, err := g.base.GetPresignedDownloadURL(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) DeleteObject(ctx context.Context, req *storagepb.DeleteObjectRequest) (*storagepb.DeleteObjectResponse, error) {
	cReq := &connect.Request[storagepb.DeleteObjectRequest]{Msg: req}
	resp, err := g.base.DeleteObject(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListObjects(ctx context.Context, req *storagepb.ListObjectsRequest) (*storagepb.ListObjectsResponse, error) {
	cReq := &connect.Request[storagepb.ListObjectsRequest]{Msg: req}
	resp, err := g.base.ListObjects(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetStorageUsage(ctx context.Context, req *storagepb.GetStorageUsageRequest) (*storagepb.GetStorageUsageResponse, error) {
	cReq := &connect.Request[storagepb.GetStorageUsageRequest]{Msg: req}
	resp, err := g.base.GetStorageUsage(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// ─── Public helpers for internal callers ──────────────────────────────

// HasStorageConfig returns true if the tenant has at least one storage config.
func (s *Server) HasStorageConfig(ctx context.Context, tenantID string) bool {
	if _, err := s.authorizeStorageTenant(ctx, storageauth.ActionConnectionRead, tenantID); err != nil {
		return false
	}
	if s.store != nil {
		return true // cloud mode with a pre-configured store
	}
	_, _, err := s.getStoreForConfig(ctx, "", tenantID)
	return err == nil
}

// UploadObject uploads data to object storage and registers the object in the DB.
// It replicates the upload_proxy.go pattern for use by other internal services.
func (s *Server) UploadObject(ctx context.Context, tenantID, purpose, filename, contentType string, data io.Reader, size int64, refType, refID string) (objectID string, err error) {
	if _, err := s.authorizeStorageTenant(ctx, storageauth.ActionUploadInternal, tenantID); err != nil {
		return "", err
	}
	upload, _, err := s.uploadDirect(ctx, tenantID, directUploadParams{
		Purpose:        purpose,
		Filename:       filename,
		ContentType:    contentType,
		SizeBytes:      size,
		ReferenceType:  refType,
		ReferenceID:    refID,
		IdempotencyKey: "internal:" + uuid.New().String(),
		Body:           data,
	})
	if err != nil {
		return "", err
	}
	return upload.ObjectID, nil
}

// GetPresignedDownloadURLForObject returns a presigned download URL for the given object ID.
func (s *Server) GetPresignedDownloadURLForObject(ctx context.Context, tenantID, objectID string) (string, error) {
	if _, err := s.authorizeStorageTenant(ctx, storageauth.ActionObjectDownload, tenantID); err != nil {
		return "", err
	}
	if s.db == nil {
		return "", fmt.Errorf("database not available")
	}

	var obj struct {
		Key      string `db:"key"`
		ConfigID string `db:"config_id"`
	}
	err := s.db.GetContext(ctx, &obj,
		`SELECT key, config_id FROM object_storage_objects WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		objectID, tenantID)
	if err != nil {
		return "", fmt.Errorf("object not found: %w", err)
	}

	store, cfg, err := s.getStoreForConfig(ctx, obj.ConfigID, tenantID)
	if err != nil {
		return "", err
	}

	bucket := ""
	if cfg != nil {
		bucket = cfg.Bucket
	}

	url, err := store.GetPresignedURL(ctx, bucket, obj.Key, presignExpiry)
	if err != nil {
		return "", errors.New("failed to generate download URL")
	}

	return url, nil
}
