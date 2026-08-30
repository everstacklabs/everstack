package v1

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/commands"
	storagecmd "github.com/everstacklabs/everstack/internal/commands/handlers/storage"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/enterprise"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/query"
	storagequery "github.com/everstacklabs/everstack/internal/query/handlers/storage"
	storagepkg "github.com/everstacklabs/everstack/internal/storage"
	storagecredentials "github.com/everstacklabs/everstack/internal/storage/credentials"
	s3store "github.com/everstacklabs/everstack/internal/storage/s3"
	"github.com/everstacklabs/everstack/internal/storageauth"
	storagepb "github.com/everstacklabs/everstack/pkg/grpc/everstack/storage/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const presignExpiry = 15 * time.Minute

func (s *Server) getSys(ctx context.Context) (*cqrs.System, error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}
	return sys, nil
}

func getStorageConfigReadModel(ctx context.Context, sys *cqrs.System, configID, tenantID string) (*storagequery.StorageConfigReadModel, error) {
	res, err := sys.QueryBus.Execute(ctx, storagequery.NewGetStorageConfigQuery(configID, tenantID))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to get storage config"))
	}

	var data interface{} = res
	if response, ok := res.(*query.Response); ok {
		data = response.Data
	}
	if data == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("storage config not found"))
	}
	config, ok := data.(*storagequery.StorageConfigReadModel)
	if !ok || config == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("invalid storage config response"))
	}
	return config, nil
}

func (s *Server) requireStorageCredentialCutover(ctx context.Context) error {
	if s.credentialStore == nil {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("storage credential backend is not configured"))
	}
	enabled, err := storagecredentials.CredentialCutoverEnabled(ctx, s.credentialStore)
	if err != nil {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("storage credential cutover state is unavailable"))
	}
	if !enabled {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("storage credential cutover is not enabled"))
	}
	return nil
}

func (s *Server) resolveStorageConfigCredentials(ctx context.Context, tenantID string, config *storagequery.StorageConfigReadModel) (storagecredentials.ProviderCredentials, error) {
	if s.credentialStore == nil {
		return storagecredentials.ProviderCredentials{}, connect.NewError(connect.CodeFailedPrecondition, errors.New("storage credential backend is not configured"))
	}
	credentials, reference, err := storagecredentials.ResolveConfigCredentials(ctx, s.credentialStore, tenantID, config.ID, config.CredentialRef)
	if err != nil {
		return storagecredentials.ProviderCredentials{}, connect.NewError(connect.CodeInternal, errors.New("failed to resolve storage credentials"))
	}
	config.CredentialRef = reference
	return credentials, nil
}

func (s *Server) getStoreForConfig(ctx context.Context, configID, tenantID string) (storagepkg.ObjectStore, *storagequery.StorageConfigReadModel, error) {
	if _, err := s.authorizeStorageTenant(ctx, storageauth.ActionConnectionRead, tenantID); err != nil {
		return nil, nil, err
	}
	if configID == "" && s.managedDefaults != nil {
		if _, err := s.managedDefaults.EnsureDefault(ctx, tenantID); err != nil {
			return nil, nil, connect.NewError(connect.CodeInternal, errors.New("failed to ensure Everstack Storage default"))
		}
	}
	if s.store != nil && configID == "" && s.managedDefaults == nil {
		// Use pre-configured store (cloud mode)
		return s.store, nil, nil
	}

	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, nil, err
	}

	// Look up the config from DB
	var q query.Query
	if configID != "" {
		q = storagequery.NewGetStorageConfigQuery(configID, tenantID)
	} else {
		// Get default config
		q = storagequery.NewListStorageConfigsQuery(tenantID)
	}

	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, nil, connect.NewError(connect.CodeInternal, errors.New("failed to get storage config"))
	}

	var cfg *storagequery.StorageConfigReadModel
	if configID != "" {
		var data interface{} = res
		if resp, ok := res.(*query.Response); ok {
			data = resp.Data
		}
		if data == nil {
			return nil, nil, connect.NewError(connect.CodeNotFound, errors.New("storage config not found"))
		}
		cfg = data.(*storagequery.StorageConfigReadModel)
	} else {
		var data interface{} = res
		if resp, ok := res.(*query.Response); ok {
			data = resp.Data
		}
		configs, ok := data.([]storagequery.StorageConfigReadModel)
		if !ok || len(configs) == 0 {
			return nil, nil, connect.NewError(connect.CodeNotFound, errors.New("no storage config found; configure storage first"))
		}
		// Find default config
		for i := range configs {
			if configs[i].IsDefault {
				cfg = &configs[i]
				break
			}
		}
		if cfg == nil {
			cfg = &configs[0]
		}
	}

	if isManagedStorageConfig(cfg) {
		if cfg.ManagementMode != storagepkg.ManagementSystem ||
			cfg.Provider != storagepkg.ProviderEverstack ||
			cfg.TenantID != tenantID ||
			strings.TrimSpace(cfg.ManagedCellID) == "" ||
			strings.TrimSpace(cfg.ManagedPathPrefix) == "" {
			return nil, nil, connect.NewError(connect.CodeInternal, errors.New("invalid managed storage connection"))
		}
		if s.managedResolver == nil {
			return nil, nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("Everstack Storage cell resolver is not configured"))
		}
		store, err := s.managedResolver.ResolveManagedStore(ctx, managedConnectionFromReadModel(cfg))
		if err != nil {
			return nil, nil, connect.NewError(connect.CodeUnavailable, errors.New("Everstack Storage cell is unavailable"))
		}
		return store, cfg, nil
	}

	credentials, err := s.resolveStorageConfigCredentials(ctx, tenantID, cfg)
	if err != nil {
		return nil, nil, err
	}

	store, err := s3store.New(ctx, s3store.Config{
		Endpoint:             cfg.Endpoint,
		Region:               storageRegionForProvider(cfg.Provider, cfg.Region),
		Bucket:               cfg.Bucket,
		AccessKeyID:          credentials.AccessKeyID,
		SecretAccessKey:      credentials.SecretAccessKey,
		PathPrefix:           cfg.PathPrefix,
		ForcePathStyle:       shouldUsePathStyle(cfg.Provider),
		DisableNativeCopy:    shouldDisableNativeCopy(cfg.Provider),
		WireChecksum:         storageWireChecksumForProvider(cfg.Provider),
		EnforceManagedEgress: enterprise.ManagedGateway(),
	})
	if err != nil {
		return nil, nil, connect.NewError(connect.CodeInternal, errors.New("failed to create storage client"))
	}

	return store, cfg, nil
}

func shouldUsePathStyle(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "minio", "r2":
		return true
	default:
		return false
	}
}

func shouldDisableNativeCopy(provider string) bool {
	// MinIO does not consistently enforce destination If-None-Match, while R2
	// uses a provider-specific beta header for destination copy conditions.
	// Blob Plane V2 uses the portable direct-read plus conditional-create
	// fallback so copy remains no-overwrite safe on both providers.
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "minio", "r2":
		return true
	default:
		return false
	}
}

func storageWireChecksumForProvider(provider string) s3store.WireChecksumMode {
	if strings.EqualFold(strings.TrimSpace(provider), "r2") {
		return s3store.WireChecksumContentMD5
	}
	return s3store.WireChecksumSHA256
}

func storageRegionForProvider(provider, region string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	r := strings.TrimSpace(region)

	// Cloudflare R2 expects "auto" signing region for S3-compatible APIs.
	if p == "r2" {
		if r != "" && !strings.EqualFold(r, "auto") {
			slog.Warn("storage: overriding non-auto region for R2 provider", "configuredRegion", r)
		}
		return "auto"
	}

	return r
}

func (s *Server) ConfigureStorage(ctx context.Context, req *connect.Request[storagepb.ConfigureStorageRequest]) (*connect.Response[storagepb.ConfigureStorageResponse], error) {
	tenantID, err := s.authorizeStorage(ctx, storageauth.ActionConnectionConfigure)
	if err != nil {
		return nil, err
	}
	if req.Msg.GetProvider() == storagepb.StorageProvider_STORAGE_PROVIDER_EVERSTACK {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("Everstack Storage connections are managed by the system"))
	}
	if s.managedDefaults != nil && req.Msg.GetIsDefault() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("Everstack Storage is the system-managed default"))
	}

	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	userID := contextkeys.GetUserID(ctx)
	credentials := storagecredentials.ProviderCredentials{
		AccessKeyID: req.Msg.GetAccessKeyId(), SecretAccessKey: req.Msg.GetSecretAccessKey(),
	}
	if err := credentials.Validate(); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if s.credentialStore == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("storage credential backend is not configured"))
	}
	if err := s.requireStorageCredentialCutover(ctx); err != nil {
		return nil, err
	}
	connection := storagepkg.ConnectionConfig{
		Provider: providerToString(req.Msg.GetProvider()), Endpoint: req.Msg.GetEndpoint(),
		Region: storageRegionForProvider(providerToString(req.Msg.GetProvider()), req.Msg.GetRegion()),
		Bucket: req.Msg.GetBucket(), PathPrefix: req.Msg.GetPathPrefix(),
		ForcePathStyle: shouldUsePathStyle(providerToString(req.Msg.GetProvider())),
	}
	if err := s.connectionVerifier.Verify(ctx, connection, credentials); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("storage connection verification failed"))
	}
	credentialRef, err := s.credentialStore.Put(ctx, tenantID, credentials)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to store storage credentials"))
	}

	cmd := storagecmd.NewConfigureStorageCommand(
		tenantID,
		providerToString(req.Msg.GetProvider()),
		req.Msg.GetEndpoint(),
		req.Msg.GetRegion(),
		req.Msg.GetBucket(),
		credentialRef,
		req.Msg.GetPathPrefix(),
		req.Msg.GetIsDefault(),
		userID,
		"",
	)

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		if !commands.EventWasPersisted(err) {
			_ = s.credentialStore.Revoke(ctx, tenantID, credentialRef)
		}
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to configure storage connection"))
	}

	return connect.NewResponse(&storagepb.ConfigureStorageResponse{
		Config: &storagepb.StorageConfig{
			Id:         cmd.ID,
			TenantId:   tenantID,
			Provider:   req.Msg.GetProvider(),
			Endpoint:   req.Msg.GetEndpoint(),
			Region:     req.Msg.GetRegion(),
			Bucket:     req.Msg.GetBucket(),
			PathPrefix: req.Msg.GetPathPrefix(),
			IsDefault:  req.Msg.GetIsDefault(),
			Enabled:    true,
			CreatedAt:  timestamppb.Now(),
			UpdatedAt:  timestamppb.Now(),
		},
	}), nil
}

func (s *Server) GetStorageConfig(ctx context.Context, req *connect.Request[storagepb.GetStorageConfigRequest]) (*connect.Response[storagepb.GetStorageConfigResponse], error) {
	tenantID, err := s.authorizeStorage(ctx, storageauth.ActionConnectionRead)
	if err != nil {
		return nil, err
	}

	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	q := storagequery.NewGetStorageConfigQuery(req.Msg.GetConfigId(), tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if res == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("storage config not found"))
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}

	rm := data.(*storagequery.StorageConfigReadModel)
	return connect.NewResponse(&storagepb.GetStorageConfigResponse{
		Config: configReadModelToProto(rm),
	}), nil
}

func (s *Server) ListStorageConfigs(ctx context.Context, req *connect.Request[storagepb.ListStorageConfigsRequest]) (*connect.Response[storagepb.ListStorageConfigsResponse], error) {
	tenantID, err := s.authorizeStorage(ctx, storageauth.ActionConnectionRead)
	if err != nil {
		return nil, err
	}
	if s.managedDefaults != nil {
		if _, err := s.managedDefaults.EnsureDefault(ctx, tenantID); err != nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("failed to ensure Everstack Storage default"))
		}
	}

	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	q := storagequery.NewListStorageConfigsQuery(tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var configs []*storagepb.StorageConfig
	if res != nil {
		var data interface{} = res
		if resp, ok := res.(*query.Response); ok {
			data = resp.Data
		}
		if list, ok := data.([]storagequery.StorageConfigReadModel); ok {
			for i := range list {
				configs = append(configs, configReadModelToProto(&list[i]))
			}
		}
	}

	return connect.NewResponse(&storagepb.ListStorageConfigsResponse{Configs: configs}), nil
}

func (s *Server) UpdateStorageConfig(ctx context.Context, req *connect.Request[storagepb.UpdateStorageConfigRequest]) (*connect.Response[storagepb.UpdateStorageConfigResponse], error) {
	tenantID, err := s.authorizeStorage(ctx, storageauth.ActionConnectionUpdate)
	if err != nil {
		return nil, err
	}

	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}
	current, err := getStorageConfigReadModel(ctx, sys, req.Msg.GetConfigId(), tenantID)
	if err != nil {
		return nil, err
	}
	if isManagedStorageConfig(current) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("Everstack Storage connections cannot be modified"))
	}
	userID := contextkeys.GetUserID(ctx)

	cmd := storagecmd.NewUpdateStorageConfigCommand(req.Msg.GetConfigId(), tenantID, userID, "")
	if req.Msg.Endpoint != nil {
		cmd.Endpoint = req.Msg.Endpoint
	}
	if req.Msg.Region != nil {
		cmd.Region = req.Msg.Region
	}
	if req.Msg.Bucket != nil {
		cmd.Bucket = req.Msg.Bucket
	}

	connection := storagepkg.ConnectionConfig{
		Provider:       current.Provider,
		Endpoint:       current.Endpoint,
		Region:         storageRegionForProvider(current.Provider, current.Region),
		Bucket:         current.Bucket,
		PathPrefix:     current.PathPrefix,
		ForcePathStyle: shouldUsePathStyle(current.Provider),
	}
	if req.Msg.Endpoint != nil {
		connection.Endpoint = *req.Msg.Endpoint
	}
	if req.Msg.Region != nil {
		connection.Region = storageRegionForProvider(current.Provider, *req.Msg.Region)
	}
	if req.Msg.Bucket != nil {
		connection.Bucket = *req.Msg.Bucket
	}
	if req.Msg.PathPrefix != nil {
		connection.PathPrefix = *req.Msg.PathPrefix
	}

	connectionChanged := req.Msg.Endpoint != nil || req.Msg.Region != nil || req.Msg.Bucket != nil || req.Msg.PathPrefix != nil
	var newCredentialRef string
	if req.Msg.AccessKeyId != nil || req.Msg.SecretAccessKey != nil {
		if req.Msg.AccessKeyId == nil || req.Msg.SecretAccessKey == nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("both access key id and secret access key are required"))
		}
		credentials := storagecredentials.ProviderCredentials{AccessKeyID: *req.Msg.AccessKeyId, SecretAccessKey: *req.Msg.SecretAccessKey}
		if err := credentials.Validate(); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		if err := s.requireStorageCredentialCutover(ctx); err != nil {
			return nil, err
		}
		if err := s.connectionVerifier.Verify(ctx, connection, credentials); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("storage connection verification failed"))
		}
		newCredentialRef, err = s.credentialStore.Put(ctx, tenantID, credentials)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("failed to store storage credentials"))
		}
		cmd.CredentialRef = &newCredentialRef
		cmd.PreviousCredentialRef = current.CredentialRef
	} else if connectionChanged {
		credentials, err := s.resolveStorageConfigCredentials(ctx, tenantID, current)
		if err != nil {
			return nil, err
		}
		if err := s.connectionVerifier.Verify(ctx, connection, credentials); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("storage connection verification failed"))
		}
	}
	if req.Msg.PathPrefix != nil {
		cmd.PathPrefix = req.Msg.PathPrefix
	}
	if req.Msg.Enabled != nil {
		cmd.Enabled = req.Msg.Enabled
	}

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		if newCredentialRef != "" && !commands.EventWasPersisted(err) {
			_ = s.credentialStore.Revoke(ctx, tenantID, newCredentialRef)
		}
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to update storage connection"))
	}

	updated := *current
	updated.Endpoint = connection.Endpoint
	updated.Region = connection.Region
	updated.Bucket = connection.Bucket
	updated.PathPrefix = connection.PathPrefix
	if req.Msg.Enabled != nil {
		updated.Enabled = *req.Msg.Enabled
	}

	return connect.NewResponse(&storagepb.UpdateStorageConfigResponse{
		Config: configReadModelToProto(&updated),
	}), nil
}

func (s *Server) DeleteStorageConfig(ctx context.Context, req *connect.Request[storagepb.DeleteStorageConfigRequest]) (*connect.Response[storagepb.DeleteStorageConfigResponse], error) {
	tenantID, err := s.authorizeStorage(ctx, storageauth.ActionConnectionDelete)
	if err != nil {
		return nil, err
	}

	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}
	current, err := getStorageConfigReadModel(ctx, sys, req.Msg.GetConfigId(), tenantID)
	if err != nil {
		return nil, err
	}
	if isManagedStorageConfig(current) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("Everstack Storage connections cannot be deleted"))
	}

	userID := contextkeys.GetUserID(ctx)

	cmd := storagecmd.NewDeleteStorageConfigCommand(req.Msg.GetConfigId(), tenantID, userID, "")
	cmd.CredentialRef = current.CredentialRef
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to delete storage connection"))
	}

	return connect.NewResponse(&storagepb.DeleteStorageConfigResponse{}), nil
}

func (s *Server) GetPresignedUploadURL(ctx context.Context, req *connect.Request[storagepb.GetPresignedUploadURLRequest]) (*connect.Response[storagepb.GetPresignedUploadURLResponse], error) {
	tenantID, err := s.authorizeStorage(ctx, storageauth.ActionUploadInitiate)
	if err != nil {
		return nil, err
	}
	if s.uploadLifecycle == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("upload lifecycle is not configured"))
	}
	if strings.TrimSpace(req.Msg.GetFilename()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("filename is required"))
	}
	if req.Msg.GetSizeBytes() < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("size_bytes cannot be negative"))
	}
	expectedChecksum := strings.ToLower(strings.TrimSpace(req.Msg.GetExpectedChecksumSha256()))
	if expectedChecksum != "" {
		if err := (storagepkg.Checksum{Algorithm: storagepkg.ChecksumSHA256, Value: expectedChecksum}).Validate(); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("expected_checksum_sha256 must be lowercase SHA-256"))
		}
	}

	store, cfg, err := s.getStoreForConfig(ctx, req.Msg.GetConfigId(), tenantID)
	if err != nil {
		return nil, err
	}

	configID := ""
	bucket := ""
	if cfg != nil {
		configID = cfg.ID
		bucket = cfg.Bucket
	}

	idempotencyKey := strings.TrimSpace(req.Msg.GetIdempotencyKey())
	if idempotencyKey == "" {
		idempotencyKey = "legacy:" + uuid.New().String()
	}
	if len(idempotencyKey) > 255 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("idempotency_key is too long"))
	}

	objectID := uuid.New().String()
	purpose := purposeToString(req.Msg.GetPurpose())
	key := storageObjectKey(cfg, tenantID, purpose, objectID, req.Msg.GetFilename())
	now := time.Now().UTC()
	quotaBytes := int64(-1)
	if limit, capped := enterprise.ResolveEntitlements(
		ctx,
		enterprise.LicenseMonitorFromContext(ctx),
	).Limit(enterprise.UsageTypeStorageBytes); capped {
		quotaBytes = limit
	}

	upload, created, err := s.uploadLifecycle.Initiate(ctx, storagepkg.InitiateUploadParams{
		ObjectID:               objectID,
		TenantID:               tenantID,
		ConfigID:               configID,
		Key:                    key,
		Filename:               req.Msg.GetFilename(),
		ContentType:            req.Msg.GetContentType(),
		ExpectedSizeBytes:      req.Msg.GetSizeBytes(),
		ExpectedChecksumSHA256: expectedChecksum,
		Purpose:                purpose,
		ReferenceID:            req.Msg.GetReferenceId(),
		ReferenceType:          req.Msg.GetReferenceType(),
		Metadata:               json.RawMessage(`{}`),
		IdempotencyKey:         idempotencyKey,
		RequestFingerprint: storageUploadRequestFingerprint(
			tenantID,
			configID,
			req.Msg,
			expectedChecksum,
		),
		QuotaBytes: quotaBytes,
		ExpiresAt:  now.Add(presignExpiry),
		Now:        now,
	})
	if errors.Is(err, storagepkg.ErrIdempotencyConflict) {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("idempotency key was already used with different upload input"))
	}
	var quotaErr *storagepkg.QuotaExceededError
	if errors.As(err, &quotaErr) {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("storage quota exceeded"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to initiate upload"))
	}
	response := &storagepb.GetPresignedUploadURLResponse{
		ObjectId:       upload.ObjectID,
		Key:            upload.Key,
		State:          uploadStateToProto(upload.State),
		IdempotencyKey: upload.IdempotencyKey,
	}
	if upload.State != storagepkg.UploadStatePending || upload.ReservationState != storagepkg.ReservationStateReserved {
		// A replay after transfer or completion returns the same logical upload,
		// but never reissues a capability that could overwrite its provider key.
		return connect.NewResponse(response), nil
	}

	uploadURLExpiry := upload.ExpiresAt.Sub(now)
	if uploadURLExpiry <= 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("upload initiation has expired; use a new idempotency key"))
	}
	if uploadURLExpiry > presignExpiry {
		uploadURLExpiry = presignExpiry
	}

	url, headers, err := store.PutPresignedURL(
		ctx,
		bucket,
		upload.Key,
		upload.ContentType,
		upload.ExpectedSizeBytes,
		uploadURLExpiry,
	)
	if err != nil {
		if created {
			if _, _, releaseErr := s.uploadLifecycle.FailInitiation(
				ctx,
				tenantID,
				upload.ObjectID,
				"presign_failed",
				time.Now().UTC(),
			); releaseErr != nil {
				return nil, connect.NewError(connect.CodeInternal, errors.New("failed to release upload reservation"))
			}
		}
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("failed to generate upload URL"))
	}

	response.UploadUrl = url
	response.Headers = headers
	response.ExpiresInSeconds = int64(uploadURLExpiry.Seconds())
	return connect.NewResponse(response), nil
}

func storageUploadRequestFingerprint(
	tenantID string,
	configID string,
	request *storagepb.GetPresignedUploadURLRequest,
	expectedChecksum string,
) string {
	parts := []string{
		tenantID,
		configID,
		request.GetFilename(),
		request.GetContentType(),
		strconv.FormatInt(request.GetSizeBytes(), 10),
		purposeToString(request.GetPurpose()),
		request.GetReferenceId(),
		request.GetReferenceType(),
		expectedChecksum,
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:])
}

type directUploadParams struct {
	Purpose        string
	Filename       string
	ContentType    string
	SizeBytes      int64
	ReferenceType  string
	ReferenceID    string
	IdempotencyKey string
	Body           io.Reader
}

// uploadDirect moves server-side and compatibility uploads through the same
// reservation, provider verification, and exact-once publication path as
// presigned clients.
func (s *Server) uploadDirect(ctx context.Context, tenantID string, params directUploadParams) (*storagepkg.Upload, string, error) {
	if s.uploadLifecycle == nil {
		return nil, "", errors.New("upload lifecycle is not configured")
	}
	if strings.TrimSpace(params.Filename) == "" || params.Body == nil || params.SizeBytes < 0 {
		return nil, "", errors.New("direct upload input is invalid")
	}
	if params.Purpose == "" {
		params.Purpose = "upload"
	}
	if params.ContentType == "" {
		params.ContentType = "application/octet-stream"
	}
	if params.IdempotencyKey == "" {
		params.IdempotencyKey = "internal:" + uuid.New().String()
	}
	if len(params.IdempotencyKey) > 255 {
		return nil, "", errors.New("direct upload idempotency key is too long")
	}

	operationCtx := storageauth.WithSystemPrincipal(ctx, tenantID)
	store, cfg, err := s.getStoreForConfig(operationCtx, "", tenantID)
	if err != nil {
		return nil, "", errors.New("storage not configured")
	}
	configID := ""
	bucket := ""
	if cfg != nil {
		configID = cfg.ID
		bucket = cfg.Bucket
	}

	objectID := uuid.New().String()
	key := storageObjectKey(cfg, tenantID, params.Purpose, objectID, params.Filename)
	now := time.Now().UTC()
	quotaBytes := int64(-1)
	if limit, capped := enterprise.ResolveEntitlements(
		ctx,
		enterprise.LicenseMonitorFromContext(ctx),
	).Limit(enterprise.UsageTypeStorageBytes); capped {
		quotaBytes = limit
	}
	fingerprintParts := []string{
		"direct:v1",
		tenantID,
		configID,
		params.Filename,
		params.ContentType,
		strconv.FormatInt(params.SizeBytes, 10),
		params.Purpose,
		params.ReferenceID,
		params.ReferenceType,
	}
	fingerprintDigest := sha256.Sum256([]byte(strings.Join(fingerprintParts, "\x00")))

	uploader := storagepkg.NewDirectUploader(store, s.uploadLifecycle, bucket)
	upload, etag, err := uploader.Upload(operationCtx, storagepkg.InitiateUploadParams{
		ObjectID:           objectID,
		TenantID:           tenantID,
		ConfigID:           configID,
		Key:                key,
		Filename:           params.Filename,
		ContentType:        params.ContentType,
		ExpectedSizeBytes:  params.SizeBytes,
		Purpose:            params.Purpose,
		ReferenceID:        params.ReferenceID,
		ReferenceType:      params.ReferenceType,
		Metadata:           json.RawMessage(`{}`),
		IdempotencyKey:     params.IdempotencyKey,
		RequestFingerprint: hex.EncodeToString(fingerprintDigest[:]),
		QuotaBytes:         quotaBytes,
		ExpiresAt:          now.Add(presignExpiry),
		Now:                now,
	}, params.Body)
	if err != nil {
		return nil, "", fmt.Errorf("initiate direct storage upload: %w", err)
	}
	return upload, etag, nil
}

func storageObjectKey(config *storagequery.StorageConfigReadModel, tenantID, purpose, objectID, filename string) string {
	if isManagedStorageConfig(config) {
		return fmt.Sprintf("v1/connections/%s/pending/%s", config.ID, objectID)
	}
	return fmt.Sprintf("tenants/%s/%s/%s/%s", tenantID, purpose, objectID, filename)
}

func (s *Server) CompleteUpload(ctx context.Context, req *connect.Request[storagepb.CompleteUploadRequest]) (*connect.Response[storagepb.CompleteUploadResponse], error) {
	tenantID, err := s.authorizeStorage(ctx, storageauth.ActionUploadComplete)
	if err != nil {
		return nil, err
	}
	if req.Msg.GetObjectId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("object_id is required"))
	}
	if s.uploadLifecycle == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("upload lifecycle is not configured"))
	}
	reportedChecksum := strings.ToLower(strings.TrimSpace(req.Msg.GetChecksumSha256()))
	if reportedChecksum != "" {
		if err := (storagepkg.Checksum{Algorithm: storagepkg.ChecksumSHA256, Value: reportedChecksum}).Validate(); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("checksum_sha256 must be a SHA-256 digest"))
		}
	}
	metadata := json.RawMessage(`{}`)
	if req.Msg.GetMetadata() != nil {
		metadata, err = json.Marshal(req.Msg.GetMetadata().AsMap())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("metadata must be valid JSON"))
		}
	}

	now := time.Now().UTC()
	upload, alreadyReady, err := s.uploadLifecycle.BeginVerification(
		ctx,
		tenantID,
		req.Msg.GetObjectId(),
		now,
		2*time.Minute,
	)
	if errors.Is(err, storagepkg.ErrUploadNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("upload not found"))
	}
	if errors.Is(err, storagepkg.ErrUploadBusy) {
		return nil, connect.NewError(connect.CodeAborted, errors.New("upload verification is already in progress"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("upload cannot be verified in its current state"))
	}
	if alreadyReady {
		return completedUploadResponse(upload), nil
	}

	if upload.ExpectedChecksumSHA256 != "" && reportedChecksum != "" && upload.ExpectedChecksumSHA256 != reportedChecksum {
		if failureErr := s.recordVerificationFailure(
			ctx,
			upload,
			storagepkg.UploadStateQuarantined,
			"checksum_expectation_conflict",
			0,
			"",
			now,
		); failureErr != nil {
			return nil, failureErr
		}
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("completion checksum differs from upload initiation"))
	}

	store, cfg, err := s.getStoreForConfig(ctx, upload.ConfigID, tenantID)
	if err != nil {
		if failureErr := s.recordVerificationFailure(ctx, upload, storagepkg.UploadStateFailed, "provider_unavailable", 0, "", now); failureErr != nil {
			return nil, failureErr
		}
		return nil, err
	}
	blobStore, err := storagepkg.RequireBlobPlane(store)
	if err != nil {
		if failureErr := s.recordVerificationFailure(ctx, upload, storagepkg.UploadStateFailed, "blob_plane_unsupported", 0, "", now); failureErr != nil {
			return nil, failureErr
		}
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("storage provider does not support verified uploads"))
	}

	bucket := ""
	if cfg != nil {
		bucket = cfg.Bucket
	}
	reader, err := blobStore.Get(ctx, bucket, upload.Key)
	if err != nil {
		failureCode := "provider_unavailable"
		responseCode := connect.CodeUnavailable
		if storagepkg.IsCode(err, storagepkg.ErrorNotFound) {
			failureCode = "object_not_found"
			responseCode = connect.CodeFailedPrecondition
		}
		if failureErr := s.recordVerificationFailure(ctx, upload, storagepkg.UploadStateFailed, failureCode, 0, "", now); failureErr != nil {
			return nil, failureErr
		}
		return nil, connect.NewError(responseCode, errors.New("uploaded object could not be verified"))
	}
	if reader == nil || reader.Body == nil {
		if failureErr := s.recordVerificationFailure(ctx, upload, storagepkg.UploadStateFailed, "provider_invalid_response", 0, "", now); failureErr != nil {
			return nil, failureErr
		}
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("storage provider returned an invalid object"))
	}
	defer reader.Body.Close()

	hasher := sha256.New()
	readLimit := upload.ExpectedSizeBytes + 1
	if upload.ExpectedSizeBytes == int64(9223372036854775807) {
		readLimit = upload.ExpectedSizeBytes
	}
	actualSize, readErr := io.Copy(hasher, io.LimitReader(reader.Body, readLimit))
	actualChecksum := hex.EncodeToString(hasher.Sum(nil))
	if readErr != nil {
		if failureErr := s.recordVerificationFailure(ctx, upload, storagepkg.UploadStateFailed, "provider_read_failed", actualSize, actualChecksum, now); failureErr != nil {
			return nil, failureErr
		}
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("uploaded object could not be read"))
	}
	if actualSize != upload.ExpectedSizeBytes || reader.SizeBytes != upload.ExpectedSizeBytes {
		if failureErr := s.recordVerificationFailure(ctx, upload, storagepkg.UploadStateQuarantined, "size_mismatch", actualSize, actualChecksum, now); failureErr != nil {
			return nil, failureErr
		}
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("uploaded object size does not match initiation"))
	}

	expectedChecksum := upload.ExpectedChecksumSHA256
	if expectedChecksum == "" {
		expectedChecksum = reportedChecksum
	}
	if expectedChecksum != "" && actualChecksum != expectedChecksum {
		if failureErr := s.recordVerificationFailure(ctx, upload, storagepkg.UploadStateQuarantined, "checksum_mismatch", actualSize, actualChecksum, now); failureErr != nil {
			return nil, failureErr
		}
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("uploaded object checksum does not match"))
	}

	ready, _, err := s.uploadLifecycle.CommitVerifiedUpload(
		ctx,
		tenantID,
		upload.ObjectID,
		upload.AttemptCount,
		storagepkg.VerifiedUpload{
			SizeBytes:      actualSize,
			ChecksumSHA256: actualChecksum,
			Metadata:       metadata,
			Now:            time.Now().UTC(),
		},
	)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to publish verified upload"))
	}
	return completedUploadResponse(ready), nil
}

func (s *Server) recordVerificationFailure(
	ctx context.Context,
	upload *storagepkg.Upload,
	state storagepkg.UploadState,
	code string,
	actualSize int64,
	actualChecksum string,
	now time.Time,
) error {
	_, err := s.uploadLifecycle.FailVerification(
		ctx,
		upload.TenantID,
		upload.ObjectID,
		upload.AttemptCount,
		storagepkg.VerificationFailure{
			State:          state,
			Code:           code,
			ActualSize:     actualSize,
			ActualChecksum: actualChecksum,
			RetryAt:        now.Add(time.Minute),
			Now:            now,
		},
	)
	if err != nil {
		return connect.NewError(connect.CodeInternal, errors.New("failed to record upload verification outcome"))
	}
	return nil
}

func completedUploadResponse(upload *storagepkg.Upload) *connect.Response[storagepb.CompleteUploadResponse] {
	object := &storagepb.StorageObject{
		Id:             upload.ObjectID,
		TenantId:       upload.TenantID,
		ConfigId:       upload.ConfigID,
		Key:            upload.Key,
		Filename:       upload.Filename,
		ContentType:    upload.ContentType,
		SizeBytes:      upload.ActualSizeBytes,
		ChecksumSha256: upload.ActualChecksumSHA256,
		Purpose:        stringToPurpose(upload.Purpose),
		ReferenceId:    upload.ReferenceID,
		ReferenceType:  upload.ReferenceType,
		CreatedAt:      timestamppb.New(upload.CreatedAt),
	}
	return connect.NewResponse(&storagepb.CompleteUploadResponse{
		Object: object,
		State:  uploadStateToProto(upload.State),
	})
}

func (s *Server) GetPresignedDownloadURL(ctx context.Context, req *connect.Request[storagepb.GetPresignedDownloadURLRequest]) (*connect.Response[storagepb.GetPresignedDownloadURLResponse], error) {
	tenantID, err := s.authorizeStorage(ctx, storageauth.ActionObjectDownload)
	if err != nil {
		return nil, err
	}

	// Look up the object to get its key and config
	if s.db == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("database not available"))
	}

	var obj struct {
		Key      string `db:"key"`
		ConfigID string `db:"config_id"`
	}
	err = s.db.GetContext(ctx, &obj,
		`SELECT key, config_id FROM object_storage_objects WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		req.Msg.GetObjectId(), tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("object not found"))
	}

	store, cfg, err := s.getStoreForConfig(ctx, obj.ConfigID, tenantID)
	if err != nil {
		return nil, err
	}

	bucket := ""
	if cfg != nil {
		bucket = cfg.Bucket
	}

	url, err := store.GetPresignedURL(ctx, bucket, obj.Key, presignExpiry)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to generate download URL"))
	}

	return connect.NewResponse(&storagepb.GetPresignedDownloadURLResponse{
		DownloadUrl:      url,
		ExpiresInSeconds: int64(presignExpiry.Seconds()),
	}), nil
}

func (s *Server) DeleteObject(ctx context.Context, req *connect.Request[storagepb.DeleteObjectRequest]) (*connect.Response[storagepb.DeleteObjectResponse], error) {
	tenantID, err := s.authorizeStorage(ctx, storageauth.ActionObjectDelete)
	if err != nil {
		return nil, err
	}
	if req.Msg.GetObjectId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("object_id is required"))
	}
	if s.uploadLifecycle == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("upload lifecycle is not configured"))
	}

	now := time.Now().UTC()
	upload, alreadyDeleted, err := s.uploadLifecycle.BeginDeletion(ctx, tenantID, req.Msg.GetObjectId(), now)
	if errors.Is(err, storagepkg.ErrUploadNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("object not found"))
	}
	if errors.Is(err, storagepkg.ErrUploadBusy) {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("storage deletion retry is scheduled"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("object cannot be deleted in its current state"))
	}
	if alreadyDeleted {
		return connect.NewResponse(&storagepb.DeleteObjectResponse{
			State: uploadStateToProto(upload.State),
		}), nil
	}

	if err := s.deleteUploadProviderBytes(ctx, upload); err != nil {
		failureCode := "provider_delete_failed"
		var operationErr *storagepkg.OperationError
		if errors.As(err, &operationErr) && operationErr.Code != "" {
			failureCode = string(operationErr.Code)
		}
		if _, failureErr := s.uploadLifecycle.FailDeletion(
			ctx,
			tenantID,
			upload.ObjectID,
			failureCode,
			now.Add(time.Minute),
			now,
		); failureErr != nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("failed to record storage deletion retry"))
		}
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("storage provider deletion failed and will be retried"))
	}

	deleted, _, err := s.uploadLifecycle.CompleteDeletion(ctx, tenantID, upload.ObjectID, time.Now().UTC())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to complete storage deletion"))
	}

	return connect.NewResponse(&storagepb.DeleteObjectResponse{
		State: uploadStateToProto(deleted.State),
	}), nil
}

func (s *Server) deleteUploadProviderBytes(ctx context.Context, upload *storagepkg.Upload) error {
	store, cfg, err := s.getStoreForConfig(ctx, upload.ConfigID, upload.TenantID)
	if err != nil {
		return storagepkg.NewOperationError("resolve provider for deletion", storagepkg.ErrorUnavailable, true, "", 0)
	}
	bucket := ""
	if cfg != nil {
		bucket = cfg.Bucket
	}
	if upload.MultipartUploadID != "" {
		blobStore, err := storagepkg.RequireBlobPlane(store)
		if err != nil {
			return err
		}
		err = blobStore.AbortMultipart(ctx, storagepkg.MultipartUpload{
			Bucket: bucket,
			Key:    upload.Key,
			ID:     upload.MultipartUploadID,
		})
		if err != nil && !storagepkg.IsCode(err, storagepkg.ErrorNotFound) {
			return err
		}
	}
	if err := store.Delete(ctx, bucket, upload.Key); err != nil && !storagepkg.IsCode(err, storagepkg.ErrorNotFound) {
		return err
	}
	return nil
}

// VerifyUpload implements storage.UploadReconcileExecutor. The regular public
// completion path remains the single verification implementation, including
// its lease, trusted digest, and durable failure recording.
func (s *Server) VerifyUpload(ctx context.Context, upload *storagepkg.Upload, _ time.Time) error {
	if upload == nil {
		return errors.New("storage reconciliation upload is missing")
	}
	if _, err := storageauth.AuthorizeTenant(ctx, storageauth.ActionAdminReconcile, upload.TenantID); err != nil {
		return err
	}
	_, err := s.CompleteUpload(ctx, connect.NewRequest(&storagepb.CompleteUploadRequest{
		TenantId: upload.TenantID,
		ObjectId: upload.ObjectID,
	}))
	if connect.CodeOf(err) == connect.CodeAborted {
		return storagepkg.ErrUploadBusy
	}
	return err
}

// DeleteUpload implements storage.UploadReconcileExecutor and includes
// multipart abort before the idempotent object delete.
func (s *Server) DeleteUpload(ctx context.Context, upload *storagepkg.Upload) error {
	if upload == nil {
		return errors.New("storage reconciliation upload is missing")
	}
	if _, err := storageauth.AuthorizeTenant(ctx, storageauth.ActionAdminReconcile, upload.TenantID); err != nil {
		return err
	}
	return s.deleteUploadProviderBytes(ctx, upload)
}

var _ storagepkg.UploadReconcileExecutor = (*Server)(nil)

func (s *Server) ListObjects(ctx context.Context, req *connect.Request[storagepb.ListObjectsRequest]) (*connect.Response[storagepb.ListObjectsResponse], error) {
	tenantID, err := s.authorizeStorage(ctx, storageauth.ActionObjectList)
	if err != nil {
		return nil, err
	}

	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	q := storagequery.NewListObjectsQuery(
		tenantID,
		purposeToString(req.Msg.GetPurpose()),
		req.Msg.GetReferenceId(),
		req.Msg.GetReferenceType(),
		int(req.Msg.GetPageSize()),
		0,
	)

	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var objects []*storagepb.StorageObject
	if res != nil {
		var data interface{} = res
		if resp, ok := res.(*query.Response); ok {
			data = resp.Data
		}
		if list, ok := data.([]storagequery.StorageObjectReadModel); ok {
			for i := range list {
				objects = append(objects, objectReadModelToProto(&list[i]))
			}
		}
	}

	return connect.NewResponse(&storagepb.ListObjectsResponse{
		Objects:    objects,
		TotalCount: int32(len(objects)),
	}), nil
}

func (s *Server) GetStorageUsage(ctx context.Context, req *connect.Request[storagepb.GetStorageUsageRequest]) (*connect.Response[storagepb.GetStorageUsageResponse], error) {
	tenantID, err := s.authorizeStorage(ctx, storageauth.ActionUsageRead)
	if err != nil {
		return nil, err
	}

	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	q := storagequery.NewGetStorageUsageQuery(tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}

	rm := data.(*storagequery.StorageUsageReadModel)
	quotaBytes := int64(-1)
	if limit, capped := enterprise.ResolveEntitlements(
		ctx,
		enterprise.LicenseMonitorFromContext(ctx),
	).Limit(enterprise.UsageTypeStorageBytes); capped {
		quotaBytes = limit
	}
	return connect.NewResponse(&storagepb.GetStorageUsageResponse{
		Usage: &storagepb.StorageUsage{
			TenantId:            rm.TenantID,
			TotalBytes:          rm.TotalBytes,
			ObjectCount:         rm.ObjectCount,
			QuotaBytes:          quotaBytes,
			UpdatedAt:           timestamppb.New(rm.UpdatedAt),
			ReservedBytes:       rm.ReservedBytes,
			ReservedObjectCount: rm.ReservedObjectCount,
		},
	}), nil
}

// --- Helpers ---

func providerToString(p storagepb.StorageProvider) string {
	switch p {
	case storagepb.StorageProvider_STORAGE_PROVIDER_S3:
		return "s3"
	case storagepb.StorageProvider_STORAGE_PROVIDER_R2:
		return "r2"
	case storagepb.StorageProvider_STORAGE_PROVIDER_MINIO:
		return "minio"
	case storagepb.StorageProvider_STORAGE_PROVIDER_GCS:
		return "gcs"
	case storagepb.StorageProvider_STORAGE_PROVIDER_EVERSTACK:
		return storagepkg.ProviderEverstack
	default:
		return "s3"
	}
}

func stringToProvider(s string) storagepb.StorageProvider {
	switch s {
	case "s3":
		return storagepb.StorageProvider_STORAGE_PROVIDER_S3
	case "r2":
		return storagepb.StorageProvider_STORAGE_PROVIDER_R2
	case "minio":
		return storagepb.StorageProvider_STORAGE_PROVIDER_MINIO
	case "gcs":
		return storagepb.StorageProvider_STORAGE_PROVIDER_GCS
	case storagepkg.ProviderEverstack:
		return storagepb.StorageProvider_STORAGE_PROVIDER_EVERSTACK
	default:
		return storagepb.StorageProvider_STORAGE_PROVIDER_UNSPECIFIED
	}
}

func purposeToString(p storagepb.ObjectPurpose) string {
	switch p {
	case storagepb.ObjectPurpose_OBJECT_PURPOSE_DATASET:
		return "dataset"
	case storagepb.ObjectPurpose_OBJECT_PURPOSE_ARTIFACT:
		return "artifact"
	case storagepb.ObjectPurpose_OBJECT_PURPOSE_UPLOAD:
		return "upload"
	case storagepb.ObjectPurpose_OBJECT_PURPOSE_EVAL_RESULT:
		return "eval_result"
	case storagepb.ObjectPurpose_OBJECT_PURPOSE_VOICE_AUDIO:
		return "voice_audio"
	default:
		return "upload"
	}
}

func stringToPurpose(s string) storagepb.ObjectPurpose {
	switch s {
	case "dataset":
		return storagepb.ObjectPurpose_OBJECT_PURPOSE_DATASET
	case "artifact":
		return storagepb.ObjectPurpose_OBJECT_PURPOSE_ARTIFACT
	case "upload":
		return storagepb.ObjectPurpose_OBJECT_PURPOSE_UPLOAD
	case "eval_result":
		return storagepb.ObjectPurpose_OBJECT_PURPOSE_EVAL_RESULT
	case "voice_audio":
		return storagepb.ObjectPurpose_OBJECT_PURPOSE_VOICE_AUDIO
	default:
		return storagepb.ObjectPurpose_OBJECT_PURPOSE_UNSPECIFIED
	}
}

func configReadModelToProto(rm *storagequery.StorageConfigReadModel) *storagepb.StorageConfig {
	if isManagedStorageConfig(rm) {
		return &storagepb.StorageConfig{
			Id:            rm.ID,
			TenantId:      rm.TenantID,
			Provider:      storagepb.StorageProvider_STORAGE_PROVIDER_EVERSTACK,
			IsDefault:     rm.IsDefault,
			Enabled:       rm.Enabled,
			CreatedAt:     timestamppb.New(rm.CreatedAt),
			UpdatedAt:     timestamppb.New(rm.UpdatedAt),
			SystemManaged: true,
		}
	}
	return &storagepb.StorageConfig{
		Id:            rm.ID,
		TenantId:      rm.TenantID,
		Provider:      stringToProvider(rm.Provider),
		Endpoint:      rm.Endpoint,
		Region:        rm.Region,
		Bucket:        rm.Bucket,
		PathPrefix:    rm.PathPrefix,
		IsDefault:     rm.IsDefault,
		Enabled:       rm.Enabled,
		CreatedAt:     timestamppb.New(rm.CreatedAt),
		UpdatedAt:     timestamppb.New(rm.UpdatedAt),
		SystemManaged: false,
	}
}

func isManagedStorageConfig(rm *storagequery.StorageConfigReadModel) bool {
	return rm != nil && (rm.ManagementMode == storagepkg.ManagementSystem || rm.Provider == storagepkg.ProviderEverstack)
}

func managedConnectionFromReadModel(rm *storagequery.StorageConfigReadModel) storagepkg.ManagedConnection {
	return storagepkg.ManagedConnection{
		ConfigID:   rm.ID,
		TenantID:   rm.TenantID,
		CellID:     rm.ManagedCellID,
		PathPrefix: rm.ManagedPathPrefix,
		CreatedAt:  rm.CreatedAt,
		UpdatedAt:  rm.UpdatedAt,
	}
}

func objectReadModelToProto(rm *storagequery.StorageObjectReadModel) *storagepb.StorageObject {
	obj := &storagepb.StorageObject{
		Id:             rm.ID,
		TenantId:       rm.TenantID,
		ConfigId:       rm.ConfigID,
		Key:            rm.Key,
		Filename:       rm.Filename,
		ContentType:    rm.ContentType,
		SizeBytes:      rm.SizeBytes,
		ChecksumSha256: rm.ChecksumSHA256,
		Purpose:        stringToPurpose(rm.Purpose),
		ReferenceId:    rm.ReferenceID,
		ReferenceType:  rm.ReferenceType,
		CreatedAt:      timestamppb.New(rm.CreatedAt),
	}
	if rm.DeletedAt.Valid {
		obj.DeletedAt = timestamppb.New(rm.DeletedAt.Time)
	}
	return obj
}
