package serve

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	storagesvc "github.com/everstacklabs/everstack/internal/api/grpc/storage/v1"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/database/dialect"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	storagepb "github.com/everstacklabs/everstack/pkg/grpc/everstack/storage/v1"
	"github.com/google/uuid"
)

func TestManagedR2StorageServiceLifecycleAcceptance(t *testing.T) {
	if strings.TrimSpace(os.Getenv("EVERSTACK_MANAGED_STORAGE_ACCEPTANCE_ENABLED")) != "1" {
		t.Skip("set EVERSTACK_MANAGED_STORAGE_ACCEPTANCE_ENABLED=1 for credentialed managed R2 acceptance")
	}
	dsn := strings.TrimSpace(os.Getenv("EVERSTACK_MANAGED_STORAGE_ACCEPTANCE_POSTGRES_DSN"))
	if dsn == "" {
		t.Fatal("EVERSTACK_MANAGED_STORAGE_ACCEPTANCE_POSTGRES_DSN is required")
	}
	// Gateway migrations are loaded from repository-relative paths at runtime.
	// The test binary starts in cmd/serve, so move to the repository root before
	// constructing the same database schema as the gateway process.
	t.Chdir("../..")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	conn, err := database.Open(ctx, database.Config{Type: database.TypePostgres, DSN: dsn})
	if err != nil {
		t.Fatal("open acceptance database")
	}
	defer conn.Close(ctx)
	if err := conn.RW.PingContext(ctx); err != nil {
		t.Fatal("ping acceptance database")
	}
	if err := (dialect.Postgres{}).EnsureSchema(ctx, conn.RW); err != nil {
		t.Fatal("migrate acceptance database")
	}

	system, err := cqrs.NewSystem(ctx, conn)
	if err != nil {
		t.Fatal("create acceptance CQRS system")
	}
	serverContext := cqrs.WithSystem(ctx, system)
	runtime, err := buildManagedStorageRuntime(
		serverContext,
		conn.RW,
		true,
		os.Getenv,
		defaultManagedStorageS3Factory,
	)
	if err != nil || runtime == nil {
		t.Fatal("configure managed storage runtime")
	}
	server := storagesvc.CreateServerWithSecurityDeps(serverContext, nil, conn.RW, nil, nil)
	server.SetManagedStorage(runtime.defaults, runtime.resolver)

	tenantID := uuid.NewString()
	requestContext := contextkeys.WithAuthenticatedAPIKey(serverContext, tenantID, "acceptance-key-hash")
	configs, err := server.ListStorageConfigs(
		requestContext,
		connect.NewRequest(&storagepb.ListStorageConfigsRequest{}),
	)
	if err != nil {
		t.Fatal("list managed storage connections")
	}
	if configs == nil || len(configs.Msg.GetConfigs()) != 1 {
		if configs == nil {
			t.Fatal("list managed storage connections returned no response")
		}
		t.Fatalf("managed storage connections = %d, want 1", len(configs.Msg.GetConfigs()))
	}
	config := configs.Msg.GetConfigs()[0]
	if config.GetProvider() != storagepb.StorageProvider_STORAGE_PROVIDER_EVERSTACK || !config.GetSystemManaged() {
		t.Fatalf("managed connection provider = %v, system managed = %t", config.GetProvider(), config.GetSystemManaged())
	}
	if config.GetEndpoint() != "" || config.GetRegion() != "" || config.GetBucket() != "" || config.GetPathPrefix() != "" {
		t.Fatal("managed connection exposed physical placement")
	}

	payload := []byte("Everstack managed storage service acceptance\n")
	digest := sha256.Sum256(payload)
	checksum := hex.EncodeToString(digest[:])
	upload, err := server.GetPresignedUploadURL(
		requestContext,
		connect.NewRequest(&storagepb.GetPresignedUploadURLRequest{
			Filename:               "acceptance.txt",
			ContentType:            "text/plain",
			SizeBytes:              int64(len(payload)),
			Purpose:                storagepb.ObjectPurpose_OBJECT_PURPOSE_ARTIFACT,
			ReferenceId:            "managed-r2-service-acceptance",
			ReferenceType:          "acceptance",
			IdempotencyKey:         "managed-r2-service-" + uuid.NewString(),
			ExpectedChecksumSha256: checksum,
		}),
	)
	if err != nil || upload == nil || upload.Msg.GetUploadUrl() == "" || upload.Msg.GetObjectId() == "" {
		t.Fatal("initiate managed storage upload")
	}

	objectKey := upload.Msg.GetKey()
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		connection, ensureErr := runtime.defaults.EnsureDefault(cleanupCtx, tenantID)
		if ensureErr != nil {
			return
		}
		store, resolveErr := runtime.resolver.ResolveManagedStore(cleanupCtx, *connection)
		if resolveErr == nil && store != nil && objectKey != "" {
			_ = store.Delete(cleanupCtx, "", objectKey)
		}
	})

	putRequest, err := http.NewRequestWithContext(ctx, http.MethodPut, upload.Msg.GetUploadUrl(), bytes.NewReader(payload))
	if err != nil {
		t.Fatal("build presigned upload request")
	}
	for name, value := range upload.Msg.GetHeaders() {
		putRequest.Header.Set(name, value)
	}
	httpClient := &http.Client{Timeout: 2 * time.Minute}
	putResponse, err := httpClient.Do(putRequest)
	if err != nil {
		t.Fatal("execute presigned upload request")
	}
	_, _ = io.Copy(io.Discard, putResponse.Body)
	_ = putResponse.Body.Close()
	if putResponse.StatusCode < http.StatusOK || putResponse.StatusCode >= http.StatusMultipleChoices {
		t.Fatalf("presigned upload status = %d", putResponse.StatusCode)
	}

	completed, err := server.CompleteUpload(
		requestContext,
		connect.NewRequest(&storagepb.CompleteUploadRequest{
			ObjectId:       upload.Msg.GetObjectId(),
			ChecksumSha256: checksum,
			SizeBytes:      int64(len(payload)),
		}),
	)
	if err != nil {
		t.Fatal("complete managed storage upload")
	}
	if completed == nil || completed.Msg.GetState() != storagepb.UploadState_UPLOAD_STATE_READY ||
		completed.Msg.GetObject().GetChecksumSha256() != checksum {
		if completed == nil {
			t.Fatal("complete managed storage upload returned no response")
		}
		t.Fatalf("completed upload state = %v", completed.Msg.GetState())
	}

	objects, err := server.ListObjects(
		requestContext,
		connect.NewRequest(&storagepb.ListObjectsRequest{
			Purpose:       storagepb.ObjectPurpose_OBJECT_PURPOSE_ARTIFACT,
			ReferenceId:   "managed-r2-service-acceptance",
			ReferenceType: "acceptance",
			PageSize:      10,
		}),
	)
	if err != nil {
		t.Fatal("list managed storage objects")
	}
	if objects == nil || len(objects.Msg.GetObjects()) != 1 || objects.Msg.GetObjects()[0].GetId() != upload.Msg.GetObjectId() {
		if objects == nil {
			t.Fatal("list managed storage objects returned no response")
		}
		t.Fatalf("listed objects = %d, want uploaded object", len(objects.Msg.GetObjects()))
	}

	otherTenantContext := contextkeys.WithAuthenticatedAPIKey(serverContext, uuid.NewString(), "other-acceptance-key-hash")
	if _, err := server.GetPresignedDownloadURL(
		otherTenantContext,
		connect.NewRequest(&storagepb.GetPresignedDownloadURLRequest{ObjectId: upload.Msg.GetObjectId()}),
	); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("cross-tenant download code = %v, want not found", connect.CodeOf(err))
	}

	download, err := server.GetPresignedDownloadURL(
		requestContext,
		connect.NewRequest(&storagepb.GetPresignedDownloadURLRequest{ObjectId: upload.Msg.GetObjectId()}),
	)
	if err != nil || download == nil || download.Msg.GetDownloadUrl() == "" {
		t.Fatal("create managed storage download URL")
	}
	downloadRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, download.Msg.GetDownloadUrl(), nil)
	if err != nil {
		t.Fatal("build presigned download request")
	}
	downloadResponse, err := httpClient.Do(downloadRequest)
	if err != nil {
		t.Fatal("execute presigned download request")
	}
	downloaded, readErr := io.ReadAll(downloadResponse.Body)
	_ = downloadResponse.Body.Close()
	if readErr != nil || downloadResponse.StatusCode != http.StatusOK {
		t.Fatalf("presigned download status = %d", downloadResponse.StatusCode)
	}
	if !bytes.Equal(downloaded, payload) {
		t.Fatal("downloaded managed object bytes differ")
	}

	deleted, err := server.DeleteObject(
		requestContext,
		connect.NewRequest(&storagepb.DeleteObjectRequest{ObjectId: upload.Msg.GetObjectId()}),
	)
	if err != nil || deleted == nil || deleted.Msg.GetState() != storagepb.UploadState_UPLOAD_STATE_DELETED {
		if deleted == nil {
			t.Fatal("delete managed storage object returned no response")
		}
		t.Fatalf("delete managed storage object state = %v", deleted.Msg.GetState())
	}
	remaining, err := server.ListObjects(
		requestContext,
		connect.NewRequest(&storagepb.ListObjectsRequest{
			Purpose:       storagepb.ObjectPurpose_OBJECT_PURPOSE_ARTIFACT,
			ReferenceId:   "managed-r2-service-acceptance",
			ReferenceType: "acceptance",
			PageSize:      10,
		}),
	)
	if err != nil {
		t.Fatal("list managed storage objects after deletion")
	}
	if remaining == nil {
		t.Fatal("list managed storage objects after deletion returned no response")
	}
	if len(remaining.Msg.GetObjects()) != 0 {
		t.Fatalf("objects after deletion = %d, want 0", len(remaining.Msg.GetObjects()))
	}
}
