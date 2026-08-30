package tools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/storage"
	"github.com/everstacklabs/everstack/internal/storageauth"
	"github.com/jmoiron/sqlx"
)

type recordingArtifactUploader struct {
	params storage.InitiateUploadParams
	body   []byte
}

func (u *recordingArtifactUploader) Upload(_ context.Context, params storage.InitiateUploadParams, body io.Reader) (*storage.Upload, string, error) {
	u.params = params
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, "", err
	}
	u.body = data
	return &storage.Upload{
		ObjectID:             params.ObjectID,
		TenantID:             params.TenantID,
		Key:                  params.Key,
		ActualSizeBytes:      int64(len(data)),
		State:                storage.UploadStateReady,
		ReservationState:     storage.ReservationStateCommitted,
		ActualChecksumSHA256: storage.NewSHA256Checksum(data).Value,
	}, "etag-1", nil
}

type authorizationProbeStore struct{}

func (authorizationProbeStore) PutPresignedURL(context.Context, string, string, string, int64, time.Duration) (string, map[string]string, error) {
	return "", nil, errors.New("object store called before authorization")
}
func (authorizationProbeStore) GetPresignedURL(context.Context, string, string, time.Duration) (string, error) {
	return "", errors.New("object store called before authorization")
}
func (authorizationProbeStore) Put(context.Context, string, string, string, io.Reader) (string, error) {
	return "", errors.New("object store called before authorization")
}
func (authorizationProbeStore) Delete(context.Context, string, string) error {
	return errors.New("object store called before authorization")
}
func (authorizationProbeStore) Head(context.Context, string, string) (int64, string, error) {
	return 0, "", errors.New("object store called before authorization")
}
func (authorizationProbeStore) List(context.Context, string, string) ([]storage.BucketObject, error) {
	return nil, errors.New("object store called before authorization")
}

func TestStorageArtifactToolsAuthorizeTenantBeforeStorageOrDatabaseAccess(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	toolContext := &StorageToolContext{
		Store:     authorizationProbeStore{},
		DB:        sqlx.NewDb(db, "sqlmock"),
		TenantID:  "tenant-1",
		SessionID: "session-1",
	}
	tests := []struct {
		name    string
		handler SyntheticToolHandler
		args    map[string]interface{}
	}{
		{
			name:    "upload artifact",
			handler: &uploadArtifactHandler{ctx: toolContext},
			args:    map[string]interface{}{"filename": "report.txt", "content": "hello"},
		},
		{
			name:    "download artifact",
			handler: &downloadArtifactHandler{ctx: toolContext},
			args:    map[string]interface{}{"object_id": "object-1"},
		},
		{
			name:    "list artifacts",
			handler: &listArtifactsHandler{ctx: toolContext},
			args:    map[string]interface{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" unauthenticated", func(t *testing.T) {
			if _, err := tt.handler.Execute(context.Background(), tt.args); !errors.Is(err, storageauth.ErrUnauthenticated) {
				t.Fatalf("Execute() error = %v, want unauthenticated", err)
			}
		})
		t.Run(tt.name+" cross tenant", func(t *testing.T) {
			ctx := contextkeys.WithAuthenticatedAPIKey(context.Background(), "tenant-2", "verified-key-hash")
			if _, err := tt.handler.Execute(ctx, tt.args); !errors.Is(err, storageauth.ErrPermissionDenied) {
				t.Fatalf("Execute() error = %v, want permission denied", err)
			}
		})
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("authorization should run before SQL: %v", err)
	}
}

func TestUploadArtifactUsesVerifiedLifecycleUploader(t *testing.T) {
	uploader := &recordingArtifactUploader{}
	handler := &uploadArtifactHandler{ctx: &StorageToolContext{
		Uploader:  uploader,
		TenantID:  "tenant-1",
		SessionID: "session-1",
		ConfigID:  "config-1",
	}}
	ctx := storageauth.WithSystemPrincipal(context.Background(), "tenant-1")

	result, err := handler.Execute(ctx, map[string]interface{}{
		"filename":     "report.txt",
		"content":      "hello",
		"purpose":      "artifact",
		"content_type": "text/plain",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if string(uploader.body) != "hello" {
		t.Fatalf("uploader body = %q, want hello", uploader.body)
	}
	if uploader.params.ReferenceID != "session-1" || uploader.params.ReferenceType != "agent_session" {
		t.Fatalf("uploader reference = %q/%q, want agent session", uploader.params.ReferenceType, uploader.params.ReferenceID)
	}
	if uploader.params.ExpectedChecksumSHA256 == "" || uploader.params.IdempotencyKey == "" {
		t.Fatalf("uploader lifecycle identity = %+v, want checksum and idempotency", uploader.params)
	}
	var response map[string]interface{}
	if err := json.Unmarshal([]byte(result), &response); err != nil {
		t.Fatalf("result JSON error = %v", err)
	}
	if response["object_id"] != uploader.params.ObjectID || response["size_bytes"] != float64(5) {
		t.Fatalf("result = %v, want verified object metadata", response)
	}
}
