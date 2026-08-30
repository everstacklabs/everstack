package v1

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/enterprise"
	"github.com/everstacklabs/everstack/internal/query"
	storagequery "github.com/everstacklabs/everstack/internal/query/handlers/storage"
	storagepkg "github.com/everstacklabs/everstack/internal/storage"
	"github.com/everstacklabs/everstack/internal/storageauth"
	storagepb "github.com/everstacklabs/everstack/pkg/grpc/everstack/storage/v1"
	"github.com/jmoiron/sqlx"
	"google.golang.org/protobuf/types/known/structpb"
)

type lifecyclePresignStore struct {
	presignCalls int
	presignErr   error
	deleteCalls  int
	deleteErr    error
}

func (s *lifecyclePresignStore) PutPresignedURL(_ context.Context, _, key, _ string, _ int64, _ time.Duration) (string, map[string]string, error) {
	s.presignCalls++
	if s.presignErr != nil {
		return "", nil, s.presignErr
	}
	return "https://storage.test/" + key, map[string]string{"x-upload": "required"}, nil
}

func (*lifecyclePresignStore) GetPresignedURL(context.Context, string, string, time.Duration) (string, error) {
	return "", errors.New("not implemented")
}

func (*lifecyclePresignStore) Put(context.Context, string, string, string, io.Reader) (string, error) {
	return "", errors.New("not implemented")
}

func (s *lifecyclePresignStore) Delete(context.Context, string, string) error {
	s.deleteCalls++
	return s.deleteErr
}

func (*lifecyclePresignStore) Head(context.Context, string, string) (int64, string, error) {
	return 0, "", errors.New("not implemented")
}

func (*lifecyclePresignStore) List(context.Context, string, string) ([]storagepkg.BucketObject, error) {
	return nil, errors.New("not implemented")
}

type lifecycleBlobStore struct {
	lifecyclePresignStore
	data       []byte
	getCalls   int
	putCalls   int
	putData    []byte
	putErr     error
	abortCalls int
	abortErr   error
}

func (*lifecycleBlobStore) BlobCapabilities() storagepkg.BlobCapabilities {
	return storagepkg.BlobCapabilities{ContractVersion: storagepkg.BlobPlaneV2, DirectRead: true, SHA256: true}
}

func (s *lifecycleBlobStore) Get(_ context.Context, _, key string) (*storagepkg.ObjectReader, error) {
	s.getCalls++
	return &storagepkg.ObjectReader{
		ObjectInfo: storagepkg.ObjectInfo{
			Key:       key,
			SizeBytes: int64(len(s.data)),
		},
		Body: io.NopCloser(bytes.NewReader(s.data)),
	}, nil
}

func (s *lifecycleBlobStore) Put(_ context.Context, _, _, _ string, body io.Reader) (string, error) {
	s.putCalls++
	if s.putErr != nil {
		return "", s.putErr
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}
	s.putData = data
	s.data = append([]byte(nil), data...)
	return "etag-1", nil
}

func (*lifecycleBlobStore) ListPage(context.Context, string, storagepkg.ListOptions) (storagepkg.ObjectPage, error) {
	return storagepkg.ObjectPage{}, errors.New("not implemented")
}

func (*lifecycleBlobStore) PutIfAbsent(context.Context, string, string, io.Reader, storagepkg.PutOptions) (storagepkg.ObjectInfo, error) {
	return storagepkg.ObjectInfo{}, errors.New("not implemented")
}

func (*lifecycleBlobStore) CopyIfAbsent(context.Context, string, string, string, string) (storagepkg.CopyResult, error) {
	return storagepkg.CopyResult{}, errors.New("not implemented")
}

func (*lifecycleBlobStore) BeginMultipart(context.Context, string, string, storagepkg.MultipartOptions) (storagepkg.MultipartUpload, error) {
	return storagepkg.MultipartUpload{}, errors.New("not implemented")
}

func (*lifecycleBlobStore) UploadPart(context.Context, storagepkg.MultipartUpload, int32, io.Reader, storagepkg.PutOptions) (storagepkg.UploadedPart, error) {
	return storagepkg.UploadedPart{}, errors.New("not implemented")
}

func (*lifecycleBlobStore) CompleteMultipart(context.Context, storagepkg.MultipartUpload, []storagepkg.UploadedPart) (storagepkg.ObjectInfo, error) {
	return storagepkg.ObjectInfo{}, errors.New("not implemented")
}

func (s *lifecycleBlobStore) AbortMultipart(context.Context, storagepkg.MultipartUpload) error {
	s.abortCalls++
	return s.abortErr
}

func TestGetUploadStatusReturnsCurrentStateAndTransitionHistory(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")

	now := time.Date(2026, time.August, 11, 11, 0, 0, 0, time.UTC)
	retryAt := now.Add(time.Minute)
	uploadColumns := []string{
		"id", "tenant_id", "config_id", "key", "filename", "content_type",
		"expected_size_bytes", "expected_checksum_sha256", "actual_size_bytes",
		"actual_checksum_sha256", "purpose", "reference_id", "reference_type",
		"metadata", "idempotency_key", "request_fingerprint", "state",
		"reservation_state", "last_error_code", "attempt_count", "last_error_at",
		"next_attempt_at", "multipart_upload_id", "expires_at", "created_at", "updated_at",
	}
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads WHERE tenant_id").WillReturnRows(
		sqlmock.NewRows(uploadColumns).AddRow(
			"object-1", "tenant-1", "config-1", "tenants/tenant-1/upload/object-1/report.txt",
			"report.txt", "text/plain", int64(256), "", int64(256), "", "upload", "", "",
			[]byte(`{}`), "request-1", "fingerprint-1", "quarantined", "reserved",
			"checksum_mismatch", int64(1), now, retryAt, "", now.Add(time.Hour), now.Add(-time.Minute), now,
		),
	)
	mock.ExpectQuery("SELECT sequence, tenant_id, object_id, from_state, to_state, reason_code, created_at").
		WillReturnRows(
			sqlmock.NewRows([]string{
				"sequence", "tenant_id", "object_id", "from_state", "to_state", "reason_code", "created_at",
			}).
				AddRow(int64(1), "tenant-1", "object-1", "", "pending", "initiated", now.Add(-2*time.Minute)).
				AddRow(int64(2), "tenant-1", "object-1", "verifying", "quarantined", "checksum_mismatch", now),
		)

	server := CreateServerWithSecurityDeps(context.Background(), nil, db, nil, nil)
	ctx := storageauth.WithSystemPrincipal(context.Background(), "tenant-1")
	response, err := server.GetUploadStatus(ctx, connect.NewRequest(&storagepb.GetUploadStatusRequest{
		TenantId: "tenant-1",
		ObjectId: "object-1",
	}))
	if err != nil {
		t.Fatalf("GetUploadStatus() error = %v", err)
	}
	if response.Msg.GetUpload().GetState() != storagepb.UploadState_UPLOAD_STATE_QUARANTINED {
		t.Fatalf("upload state = %v, want quarantined", response.Msg.GetUpload().GetState())
	}
	if got := response.Msg.GetUpload().GetLastErrorCode(); got != "checksum_mismatch" {
		t.Fatalf("last error = %q, want checksum_mismatch", got)
	}
	transitions := response.Msg.GetUpload().GetTransitions()
	if len(transitions) != 2 || transitions[1].GetReasonCode() != "checksum_mismatch" {
		t.Fatalf("transitions = %+v, want visible verification failure", transitions)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetPresignedUploadURLReplayReservesOnceAndReturnsOneLogicalUpload(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	store := &lifecyclePresignStore{}

	now := time.Now().UTC()
	expiresAt := now.Add(time.Hour)
	fingerprintParts := []string{
		"tenant-1", "", "report.txt", "text/plain", strconv.FormatInt(256, 10),
		"upload", "", "", "",
	}
	fingerprintDigest := sha256.Sum256([]byte(strings.Join(fingerprintParts, "\x00")))
	fingerprint := hex.EncodeToString(fingerprintDigest[:])
	uploadRows := func() *sqlmock.Rows {
		return sqlmock.NewRows(storageUploadLifecycleColumns()).AddRow(
			"object-1", "tenant-1", "", "tenants/tenant-1/upload/object-1/report.txt",
			"report.txt", "text/plain", int64(256), "", int64(0), "", "upload", "", "",
			[]byte(`{}`), "request-1", fingerprint, "pending", "reserved", "", int64(0),
			nil, nil, "", expiresAt, now, now,
		)
	}

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO object_storage_uploads").WillReturnRows(uploadRows())
	mock.ExpectExec("INSERT INTO object_storage_usage").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT total_bytes, object_count, reserved_bytes, reserved_object_count").
		WillReturnRows(sqlmock.NewRows([]string{
			"total_bytes", "object_count", "reserved_bytes", "reserved_object_count",
		}).AddRow(0, 0, 0, 0))
	mock.ExpectExec("UPDATE object_storage_usage").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO object_storage_upload_events").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	server := CreateServerWithSecurityDeps(context.Background(), store, db, nil, nil)
	ctx := storageauth.WithSystemPrincipal(context.Background(), "tenant-1")
	request := connect.NewRequest(&storagepb.GetPresignedUploadURLRequest{
		TenantId:       "tenant-1",
		Filename:       "report.txt",
		ContentType:    "text/plain",
		SizeBytes:      256,
		Purpose:        storagepb.ObjectPurpose_OBJECT_PURPOSE_UPLOAD,
		IdempotencyKey: "request-1",
	})
	first, err := server.GetPresignedUploadURL(ctx, request)
	if err != nil {
		t.Fatalf("first GetPresignedUploadURL() error = %v", err)
	}
	if first.Msg.GetObjectId() != "object-1" || first.Msg.GetState() != storagepb.UploadState_UPLOAD_STATE_PENDING {
		t.Fatalf("first upload = %+v, want object-1 pending", first.Msg)
	}

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO object_storage_uploads").
		WillReturnRows(sqlmock.NewRows(storageUploadLifecycleColumns()))
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads").WillReturnRows(uploadRows())
	mock.ExpectCommit()

	replayed, err := server.GetPresignedUploadURL(ctx, request)
	if err != nil {
		t.Fatalf("replayed GetPresignedUploadURL() error = %v", err)
	}
	if replayed.Msg.GetObjectId() != first.Msg.GetObjectId() {
		t.Fatalf("replayed object id = %q, want %q", replayed.Msg.GetObjectId(), first.Msg.GetObjectId())
	}
	if store.presignCalls != 2 {
		t.Fatalf("presign calls = %d, want a fresh URL for each response", store.presignCalls)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetPresignedUploadURLReadyReplayDoesNotIssueWriteCapability(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	store := &lifecyclePresignStore{}
	request := connect.NewRequest(&storagepb.GetPresignedUploadURLRequest{
		TenantId:       "tenant-1",
		Filename:       "report.txt",
		ContentType:    "text/plain",
		SizeBytes:      256,
		Purpose:        storagepb.ObjectPurpose_OBJECT_PURPOSE_UPLOAD,
		IdempotencyKey: "request-1",
	})
	fingerprint := storageUploadRequestFingerprint("tenant-1", "", request.Msg, "")
	now := time.Now().UTC()
	readyRows := sqlmock.NewRows(storageUploadLifecycleColumns()).AddRow(
		"object-1", "tenant-1", "", "tenants/tenant-1/upload/object-1/report.txt",
		"report.txt", "text/plain", int64(256), "", int64(256), strings.Repeat("0", 64),
		"upload", "", "", []byte(`{}`), "request-1", fingerprint, "ready", "committed",
		"", int64(1), nil, nil, "", now.Add(-time.Minute), now.Add(-time.Hour), now,
	)

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO object_storage_uploads").
		WillReturnRows(sqlmock.NewRows(storageUploadLifecycleColumns()))
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads").WillReturnRows(readyRows)
	mock.ExpectCommit()

	server := CreateServerWithSecurityDeps(context.Background(), store, db, nil, nil)
	ctx := storageauth.WithSystemPrincipal(context.Background(), "tenant-1")
	response, err := server.GetPresignedUploadURL(ctx, request)
	if err != nil {
		t.Fatalf("GetPresignedUploadURL() ready replay error = %v", err)
	}
	if response.Msg.GetState() != storagepb.UploadState_UPLOAD_STATE_READY || response.Msg.GetObjectId() != "object-1" {
		t.Fatalf("ready replay = %+v, want original ready object", response.Msg)
	}
	if response.Msg.GetUploadUrl() != "" || store.presignCalls != 0 {
		t.Fatalf("ready replay exposed write capability %q with %d provider calls", response.Msg.GetUploadUrl(), store.presignCalls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteUploadStreamsTrustedBytesAndReadyReplayDoesNotChargeAgain(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	store := &lifecycleBlobStore{data: []byte("hello")}

	now := time.Date(2026, time.August, 11, 11, 10, 0, 0, time.UTC)
	digest := sha256.Sum256(store.data)
	checksum := hex.EncodeToString(digest[:])
	pending := func() *sqlmock.Rows {
		return storageUploadLifecycleRow(
			"object-1",
			uploadStateValues{State: "pending", Reservation: "reserved"},
			5,
			0,
			"",
			0,
			now.Add(-time.Minute),
		)
	}
	verifying := func() *sqlmock.Rows {
		return storageUploadLifecycleRow(
			"object-1",
			uploadStateValues{State: "verifying", Reservation: "reserved"},
			5,
			0,
			"",
			1,
			now,
		)
	}
	ready := func() *sqlmock.Rows {
		return storageUploadLifecycleRow(
			"object-1",
			uploadStateValues{State: "ready", Reservation: "committed"},
			5,
			5,
			checksum,
			1,
			now.Add(time.Second),
		)
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(pending())
	mock.ExpectExec("INSERT INTO object_storage_upload_events").WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectExec("INSERT INTO object_storage_upload_events").WillReturnResult(sqlmock.NewResult(3, 1))
	mock.ExpectQuery("UPDATE object_storage_uploads").WillReturnRows(verifying())
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(verifying())
	mock.ExpectExec("INSERT INTO object_storage_objects").WithArgs(
		"object-1",
		"tenant-1",
		"",
		"tenants/tenant-1/upload/object-1/report.txt",
		"report.txt",
		"text/plain",
		int64(5),
		checksum,
		"upload",
		"",
		"",
		[]byte(`{"source":"client"}`),
		sqlmock.AnyArg(),
	).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE object_storage_usage").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO object_storage_upload_events").WillReturnResult(sqlmock.NewResult(4, 1))
	mock.ExpectQuery("UPDATE object_storage_uploads").WillReturnRows(ready())
	mock.ExpectCommit()

	server := CreateServerWithSecurityDeps(context.Background(), store, db, nil, nil)
	ctx := storageauth.WithSystemPrincipal(context.Background(), "tenant-1")
	metadata, err := structpb.NewStruct(map[string]interface{}{"source": "client"})
	if err != nil {
		t.Fatal(err)
	}
	request := connect.NewRequest(&storagepb.CompleteUploadRequest{
		TenantId:       "tenant-1",
		ObjectId:       "object-1",
		SizeBytes:      5,
		ChecksumSha256: checksum,
		Metadata:       metadata,
	})
	response, err := server.CompleteUpload(ctx, request)
	if err != nil {
		t.Fatalf("CompleteUpload() error = %v; database expectations = %v", err, mock.ExpectationsWereMet())
	}
	if response.Msg.GetState() != storagepb.UploadState_UPLOAD_STATE_READY {
		t.Fatalf("completion state = %v, want ready", response.Msg.GetState())
	}
	if response.Msg.GetObject().GetSizeBytes() != 5 || response.Msg.GetObject().GetChecksumSha256() != checksum {
		t.Fatalf("completed object = %+v, want provider-measured size and checksum", response.Msg.GetObject())
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(ready())
	mock.ExpectCommit()

	replayed, err := server.CompleteUpload(ctx, request)
	if err != nil {
		t.Fatalf("replayed CompleteUpload() error = %v", err)
	}
	if replayed.Msg.GetState() != storagepb.UploadState_UPLOAD_STATE_READY || store.getCalls != 1 {
		t.Fatalf("replayed completion = %+v, provider reads=%d", replayed.Msg, store.getCalls)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteUploadQuarantinesProviderBytesWhenChecksumDiffers(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	store := &lifecycleBlobStore{data: []byte("hello")}

	now := time.Now().UTC()
	actualDigest := sha256.Sum256(store.data)
	actualChecksum := hex.EncodeToString(actualDigest[:])
	reportedDigest := sha256.Sum256([]byte("different"))
	reportedChecksum := hex.EncodeToString(reportedDigest[:])
	pending := func() *sqlmock.Rows {
		return storageUploadLifecycleRow(
			"object-1",
			uploadStateValues{State: "pending", Reservation: "reserved"},
			5,
			0,
			"",
			0,
			now.Add(-time.Minute),
		)
	}
	verifying := func() *sqlmock.Rows {
		return storageUploadLifecycleRow(
			"object-1",
			uploadStateValues{State: "verifying", Reservation: "reserved"},
			5,
			0,
			"",
			1,
			now,
		)
	}
	quarantined := func() *sqlmock.Rows {
		return storageUploadLifecycleRow(
			"object-1",
			uploadStateValues{State: "quarantined", Reservation: "reserved"},
			5,
			5,
			actualChecksum,
			1,
			now.Add(time.Second),
		)
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(pending())
	mock.ExpectExec("INSERT INTO object_storage_upload_events").WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectExec("INSERT INTO object_storage_upload_events").WillReturnResult(sqlmock.NewResult(3, 1))
	mock.ExpectQuery("UPDATE object_storage_uploads").WillReturnRows(verifying())
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(verifying())
	mock.ExpectExec("INSERT INTO object_storage_upload_events").
		WithArgs("tenant-1", "object-1", "verifying", "quarantined", "checksum_mismatch", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(4, 1))
	mock.ExpectQuery("UPDATE object_storage_uploads").WillReturnRows(quarantined())
	mock.ExpectCommit()

	server := CreateServerWithSecurityDeps(context.Background(), store, db, nil, nil)
	ctx := storageauth.WithSystemPrincipal(context.Background(), "tenant-1")
	_, err = server.CompleteUpload(ctx, connect.NewRequest(&storagepb.CompleteUploadRequest{
		TenantId:       "tenant-1",
		ObjectId:       "object-1",
		SizeBytes:      5,
		ChecksumSha256: reportedChecksum,
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("CompleteUpload() code = %v, want failed_precondition (err=%v)", connect.CodeOf(err), err)
	}
	if store.getCalls != 1 {
		t.Fatalf("provider reads = %d, want 1", store.getCalls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetPresignedUploadURLProviderFailureReleasesNewReservation(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	store := &lifecyclePresignStore{presignErr: errors.New("provider unavailable")}
	now := time.Now().UTC()

	pending := func() *sqlmock.Rows {
		return storageUploadLifecycleRow(
			"object-1",
			uploadStateValues{State: "pending", Reservation: "reserved"},
			256,
			0,
			"",
			0,
			now,
		)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO object_storage_uploads").WillReturnRows(pending())
	mock.ExpectExec("INSERT INTO object_storage_usage").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT total_bytes, object_count, reserved_bytes, reserved_object_count").
		WillReturnRows(sqlmock.NewRows([]string{
			"total_bytes", "object_count", "reserved_bytes", "reserved_object_count",
		}).AddRow(0, 0, 0, 0))
	mock.ExpectExec("UPDATE object_storage_usage").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO object_storage_upload_events").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(pending())
	mock.ExpectExec("UPDATE object_storage_usage").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO object_storage_upload_events").WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectQuery("UPDATE object_storage_uploads").WillReturnRows(
		storageUploadInitiationFailureRow("object-1", now),
	)
	mock.ExpectCommit()

	server := CreateServerWithSecurityDeps(context.Background(), store, db, nil, nil)
	ctx := storageauth.WithSystemPrincipal(context.Background(), "tenant-1")
	_, err = server.GetPresignedUploadURL(ctx, connect.NewRequest(&storagepb.GetPresignedUploadURLRequest{
		TenantId:       "tenant-1",
		Filename:       "report.txt",
		ContentType:    "text/plain",
		SizeBytes:      256,
		Purpose:        storagepb.ObjectPurpose_OBJECT_PURPOSE_UPLOAD,
		IdempotencyKey: "request-1",
	}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("GetPresignedUploadURL() code = %v, want unavailable (err=%v)", connect.CodeOf(err), err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetStorageUsageIncludesReservedCapacityAndResolvedQuota(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	now := time.Date(2026, time.August, 11, 11, 20, 0, 0, time.UTC)

	mock.ExpectQuery("SELECT tenant_id, total_bytes, object_count, reserved_bytes, reserved_object_count, updated_at").
		WithArgs("tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"tenant_id", "total_bytes", "object_count", "reserved_bytes", "reserved_object_count", "updated_at",
		}).AddRow("tenant-1", 100, 1, 25, 1, now))

	queryBus := query.NewQueryBus()
	queryBus.RegisterHandler(storagequery.NewGetStorageUsageHandler(db))
	ctx := storageauth.WithSystemPrincipal(context.Background(), "tenant-1")
	ctx = cqrs.WithSystem(ctx, &cqrs.System{QueryBus: queryBus})
	server := CreateServerWithSecurityDeps(context.Background(), nil, db, nil, nil)

	response, err := server.GetStorageUsage(ctx, connect.NewRequest(&storagepb.GetStorageUsageRequest{
		TenantId: "tenant-1",
	}))
	if err != nil {
		t.Fatalf("GetStorageUsage() error = %v", err)
	}
	usage := response.Msg.GetUsage()
	if usage.GetReservedBytes() != 25 || usage.GetReservedObjectCount() != 1 {
		t.Fatalf("reserved usage = (%d bytes, %d objects), want (25, 1)", usage.GetReservedBytes(), usage.GetReservedObjectCount())
	}
	wantQuota, capped := enterprise.ResolveEntitlements(ctx, enterprise.LicenseMonitorFromContext(ctx)).Limit(enterprise.UsageTypeStorageBytes)
	if !capped {
		wantQuota = -1
	}
	if usage.GetQuotaBytes() != wantQuota {
		t.Fatalf("quota bytes = %d, want resolved entitlement %d", usage.GetQuotaBytes(), wantQuota)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteObjectDeletesProviderBytesAndAccountsExactlyOnce(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	store := &lifecyclePresignStore{}
	now := time.Now().UTC()
	checksum := strings.Repeat("0", 64)
	ready := func() *sqlmock.Rows {
		return storageUploadLifecycleRow(
			"object-1",
			uploadStateValues{State: "ready", Reservation: "committed"},
			5,
			5,
			checksum,
			1,
			now.Add(-time.Minute),
		)
	}
	deleting := func() *sqlmock.Rows {
		return storageUploadLifecycleRow(
			"object-1",
			uploadStateValues{State: "deleting", Reservation: "committed"},
			5,
			5,
			checksum,
			1,
			now,
		)
	}
	deleted := func() *sqlmock.Rows {
		return storageUploadLifecycleRow(
			"object-1",
			uploadStateValues{State: "deleted", Reservation: "released"},
			5,
			5,
			checksum,
			1,
			now.Add(time.Second),
		)
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(ready())
	mock.ExpectExec("INSERT INTO object_storage_upload_events").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("UPDATE object_storage_uploads").WillReturnRows(deleting())
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(deleting())
	mock.ExpectExec("UPDATE object_storage_objects").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE object_storage_usage").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO object_storage_upload_events").WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectQuery("UPDATE object_storage_uploads").WillReturnRows(deleted())
	mock.ExpectCommit()

	server := CreateServerWithSecurityDeps(context.Background(), store, db, nil, nil)
	ctx := storageauth.WithSystemPrincipal(context.Background(), "tenant-1")
	request := connect.NewRequest(&storagepb.DeleteObjectRequest{TenantId: "tenant-1", ObjectId: "object-1"})
	response, err := server.DeleteObject(ctx, request)
	if err != nil {
		t.Fatalf("DeleteObject() error = %v", err)
	}
	if response.Msg.GetState() != storagepb.UploadState_UPLOAD_STATE_DELETED {
		t.Fatalf("DeleteObject() state = %v, want deleted", response.Msg.GetState())
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(deleted())
	mock.ExpectCommit()
	replayed, err := server.DeleteObject(ctx, request)
	if err != nil {
		t.Fatalf("replayed DeleteObject() error = %v", err)
	}
	if replayed.Msg.GetState() != storagepb.UploadState_UPLOAD_STATE_DELETED || store.deleteCalls != 1 {
		t.Fatalf("replayed deletion = %+v, provider deletes=%d", replayed.Msg, store.deleteCalls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteObjectProviderFailureKeepsAccountingAndSchedulesVisibleRetry(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	store := &lifecyclePresignStore{deleteErr: errors.New("provider unavailable")}
	now := time.Now().UTC()
	checksum := strings.Repeat("0", 64)
	ready := func() *sqlmock.Rows {
		return storageUploadLifecycleRow(
			"object-1",
			uploadStateValues{State: "ready", Reservation: "committed"},
			5,
			5,
			checksum,
			1,
			now.Add(-time.Minute),
		)
	}
	deleting := func() *sqlmock.Rows {
		return storageUploadLifecycleRow(
			"object-1",
			uploadStateValues{State: "deleting", Reservation: "committed"},
			5,
			5,
			checksum,
			1,
			now,
		)
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(ready())
	mock.ExpectExec("INSERT INTO object_storage_upload_events").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("UPDATE object_storage_uploads").WillReturnRows(deleting())
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(deleting())
	mock.ExpectExec("INSERT INTO object_storage_upload_events").
		WithArgs("tenant-1", "object-1", "deleting", "deleting", "provider_delete_failed", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectQuery("UPDATE object_storage_uploads").WillReturnRows(deleting())
	mock.ExpectCommit()

	server := CreateServerWithSecurityDeps(context.Background(), store, db, nil, nil)
	ctx := storageauth.WithSystemPrincipal(context.Background(), "tenant-1")
	_, err = server.DeleteObject(ctx, connect.NewRequest(&storagepb.DeleteObjectRequest{
		TenantId: "tenant-1",
		ObjectId: "object-1",
	}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("DeleteObject() code = %v, want unavailable (err=%v)", connect.CodeOf(err), err)
	}
	if store.deleteCalls != 1 {
		t.Fatalf("provider deletes = %d, want 1", store.deleteCalls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReconciledDeletionAbortsMultipartBeforeDeletingObject(t *testing.T) {
	store := &lifecycleBlobStore{}
	server := CreateServerWithSecurityDeps(context.Background(), store, nil, nil, nil)
	ctx := storageauth.WithSystemPrincipal(context.Background(), "tenant-1")
	upload := &storagepkg.Upload{
		TenantID:          "tenant-1",
		ObjectID:          "object-1",
		Key:               "tenants/tenant-1/upload/object-1/report.txt",
		MultipartUploadID: "multipart-1",
	}

	if err := server.DeleteUpload(ctx, upload); err != nil {
		t.Fatalf("DeleteUpload() error = %v", err)
	}
	if store.abortCalls != 1 || store.deleteCalls != 1 {
		t.Fatalf("DeleteUpload() aborts=%d deletes=%d, want one of each", store.abortCalls, store.deleteCalls)
	}
}

func TestUploadObjectMigratesInternalCallersThroughVerifiedLifecycle(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	db := sqlx.NewDb(rawDB, "sqlmock")
	store := &lifecycleBlobStore{}
	now := time.Now().UTC()
	digest := sha256.Sum256([]byte("hello"))
	checksum := hex.EncodeToString(digest[:])
	pending := func() *sqlmock.Rows {
		return storageUploadLifecycleRow(
			"object-1",
			uploadStateValues{State: "pending", Reservation: "reserved"},
			5,
			0,
			"",
			0,
			now,
		)
	}
	verifying := func() *sqlmock.Rows {
		return storageUploadLifecycleRow(
			"object-1",
			uploadStateValues{State: "verifying", Reservation: "reserved"},
			5,
			0,
			"",
			1,
			now,
		)
	}
	ready := func() *sqlmock.Rows {
		return storageUploadLifecycleRow(
			"object-1",
			uploadStateValues{State: "ready", Reservation: "committed"},
			5,
			5,
			checksum,
			1,
			now,
		)
	}

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO object_storage_uploads").WillReturnRows(pending())
	mock.ExpectExec("INSERT INTO object_storage_usage").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT total_bytes, object_count, reserved_bytes, reserved_object_count").
		WillReturnRows(sqlmock.NewRows([]string{
			"total_bytes", "object_count", "reserved_bytes", "reserved_object_count",
		}).AddRow(0, 0, 0, 0))
	mock.ExpectExec("UPDATE object_storage_usage").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO object_storage_upload_events").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(pending())
	mock.ExpectExec("INSERT INTO object_storage_upload_events").WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectExec("INSERT INTO object_storage_upload_events").WillReturnResult(sqlmock.NewResult(3, 1))
	mock.ExpectQuery("UPDATE object_storage_uploads").WillReturnRows(verifying())
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(verifying())
	mock.ExpectExec("INSERT INTO object_storage_objects").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE object_storage_usage").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO object_storage_upload_events").WillReturnResult(sqlmock.NewResult(4, 1))
	mock.ExpectQuery("UPDATE object_storage_uploads").WillReturnRows(ready())
	mock.ExpectCommit()

	server := CreateServerWithSecurityDeps(context.Background(), store, db, nil, nil)
	ctx := storageauth.WithSystemPrincipal(context.Background(), "tenant-1")
	objectID, err := server.UploadObject(
		ctx,
		"tenant-1",
		"artifact",
		"report.txt",
		"text/plain",
		strings.NewReader("hello"),
		5,
		"run",
		"run-1",
	)
	if err != nil {
		t.Fatalf("UploadObject() error = %v", err)
	}
	if objectID != "object-1" || string(store.putData) != "hello" || store.getCalls != 1 {
		t.Fatalf("UploadObject() id=%q put=%q provider reads=%d", objectID, store.putData, store.getCalls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func storageUploadLifecycleColumns() []string {
	return []string{
		"id", "tenant_id", "config_id", "key", "filename", "content_type",
		"expected_size_bytes", "expected_checksum_sha256", "actual_size_bytes",
		"actual_checksum_sha256", "purpose", "reference_id", "reference_type",
		"metadata", "idempotency_key", "request_fingerprint", "state",
		"reservation_state", "last_error_code", "attempt_count", "last_error_at",
		"next_attempt_at", "multipart_upload_id", "expires_at", "created_at", "updated_at",
	}
}

type uploadStateValues struct {
	State       string
	Reservation string
}

func storageUploadLifecycleRow(
	objectID string,
	state uploadStateValues,
	expectedSize int64,
	actualSize int64,
	actualChecksum string,
	attempt int64,
	updatedAt time.Time,
) *sqlmock.Rows {
	return sqlmock.NewRows(storageUploadLifecycleColumns()).AddRow(
		objectID,
		"tenant-1",
		"",
		"tenants/tenant-1/upload/"+objectID+"/report.txt",
		"report.txt",
		"text/plain",
		expectedSize,
		"",
		actualSize,
		actualChecksum,
		"upload",
		"",
		"",
		[]byte(`{}`),
		"request-1",
		"fingerprint-1",
		state.State,
		state.Reservation,
		"",
		attempt,
		nil,
		nil,
		"",
		updatedAt.Add(time.Hour),
		updatedAt.Add(-time.Hour),
		updatedAt,
	)
}

func storageUploadInitiationFailureRow(objectID string, updatedAt time.Time) *sqlmock.Rows {
	return sqlmock.NewRows(storageUploadLifecycleColumns()).AddRow(
		objectID,
		"tenant-1",
		"",
		"tenants/tenant-1/upload/"+objectID+"/report.txt",
		"report.txt",
		"text/plain",
		int64(256),
		"",
		int64(0),
		"",
		"upload",
		"",
		"",
		[]byte(`{}`),
		"request-1",
		"fingerprint-1",
		"failed",
		"released",
		"presign_failed",
		int64(0),
		updatedAt,
		nil,
		"",
		updatedAt.Add(time.Hour),
		updatedAt.Add(-time.Hour),
		updatedAt,
	)
}
