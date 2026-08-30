package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestPostgresUploadLifecycleInitiateIsIdempotentAndReservesQuotaOnce(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	repository := NewPostgresUploadLifecycle(sqlx.NewDb(rawDB, "sqlmock"))
	now := time.Date(2026, time.August, 11, 10, 0, 0, 0, time.UTC)
	params := InitiateUploadParams{
		ObjectID:           "object-1",
		TenantID:           "tenant-1",
		ConfigID:           "config-1",
		Key:                "tenants/tenant-1/upload/object-1/report.txt",
		Filename:           "report.txt",
		ContentType:        "text/plain",
		ExpectedSizeBytes:  256,
		Purpose:            "upload",
		IdempotencyKey:     "request-1",
		RequestFingerprint: "fingerprint-1",
		QuotaBytes:         1_024,
		ExpiresAt:          now.Add(time.Hour),
		Now:                now,
	}

	uploadRows := func() *sqlmock.Rows {
		return sqlmock.NewRows(uploadLifecycleColumns()).AddRow(
			params.ObjectID, params.TenantID, params.ConfigID, params.Key,
			params.Filename, params.ContentType, params.ExpectedSizeBytes, "",
			int64(0), "", params.Purpose, "", "", []byte(`{}`),
			params.IdempotencyKey, params.RequestFingerprint, string(UploadStatePending),
			string(ReservationStateReserved), "", int64(0), nil, nil, "",
			params.ExpiresAt, now, now,
		)
	}

	// The first request creates one logical upload and reserves its expected
	// bytes before a presigned URL can be returned.
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO object_storage_uploads").WillReturnRows(uploadRows())
	mock.ExpectExec("INSERT INTO object_storage_usage").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT total_bytes, object_count, reserved_bytes, reserved_object_count").
		WillReturnRows(sqlmock.NewRows([]string{"total_bytes", "object_count", "reserved_bytes", "reserved_object_count"}).AddRow(100, 1, 0, 0))
	mock.ExpectExec("UPDATE object_storage_usage").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO object_storage_upload_events").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	first, created, err := repository.Initiate(context.Background(), params)
	if err != nil {
		t.Fatalf("first Initiate() error = %v", err)
	}
	if !created {
		t.Fatal("first Initiate() created = false, want true")
	}
	if first.State != UploadStatePending || first.ReservationState != ReservationStateReserved {
		t.Fatalf("first upload = %+v, want pending with a reservation", first)
	}

	// A replay sees the existing row. It does not touch the usage row or emit
	// a second transition, which is the exact-once accounting boundary.
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO object_storage_uploads").
		WillReturnRows(sqlmock.NewRows(uploadLifecycleColumns()))
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads").WillReturnRows(uploadRows())
	mock.ExpectCommit()

	second, created, err := repository.Initiate(context.Background(), params)
	if err != nil {
		t.Fatalf("replayed Initiate() error = %v", err)
	}
	if created {
		t.Fatal("replayed Initiate() created = true, want false")
	}
	if second.ObjectID != first.ObjectID {
		t.Fatalf("replayed object id = %q, want %q", second.ObjectID, first.ObjectID)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresUploadLifecycleInitiateRetryReacquiresReleasedPresignReservation(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	repository := NewPostgresUploadLifecycle(sqlx.NewDb(rawDB, "sqlmock"))
	now := time.Date(2026, time.August, 11, 10, 2, 0, 0, time.UTC)
	params := InitiateUploadParams{
		ObjectID:           "discarded-replay-id",
		TenantID:           "tenant-1",
		ConfigID:           "config-1",
		Key:                "discarded-replay-key",
		Filename:           "report.txt",
		ContentType:        "text/plain",
		ExpectedSizeBytes:  256,
		Purpose:            "upload",
		IdempotencyKey:     "request-1",
		RequestFingerprint: "fingerprint-1",
		QuotaBytes:         1_024,
		ExpiresAt:          now.Add(time.Hour),
		Now:                now,
	}

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO object_storage_uploads").
		WillReturnRows(sqlmock.NewRows(uploadLifecycleColumns()))
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads").WillReturnRows(
		uploadLifecycleInitiationFailureTestRow("object-1", now.Add(-time.Minute)),
	)
	mock.ExpectExec("INSERT INTO object_storage_usage").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT total_bytes, object_count, reserved_bytes, reserved_object_count").
		WillReturnRows(sqlmock.NewRows([]string{
			"total_bytes", "object_count", "reserved_bytes", "reserved_object_count",
		}).AddRow(100, 1, 0, 0))
	mock.ExpectExec("UPDATE object_storage_usage").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO object_storage_upload_events").
		WithArgs("tenant-1", "object-1", "failed", "pending", "initiation_retried", now).
		WillReturnResult(sqlmock.NewResult(9, 1))
	mock.ExpectQuery("UPDATE object_storage_uploads").WillReturnRows(
		uploadLifecycleTestRow("object-1", UploadStatePending, ReservationStateReserved, 0, now),
	)
	mock.ExpectCommit()

	upload, reservationAcquired, err := repository.Initiate(context.Background(), params)
	if err != nil {
		t.Fatalf("retried Initiate() error = %v", err)
	}
	if !reservationAcquired {
		t.Fatal("retried Initiate() reservationAcquired = false, want true")
	}
	if upload.ObjectID != "object-1" || upload.State != UploadStatePending || upload.ReservationState != ReservationStateReserved {
		t.Fatalf("retried upload = %+v, want original object pending with reservation", upload)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresUploadLifecycleBeginVerificationRecordsTransferAndLeasesTheAttempt(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	repository := NewPostgresUploadLifecycle(sqlx.NewDb(rawDB, "sqlmock"))
	now := time.Date(2026, time.August, 11, 10, 5, 0, 0, time.UTC)
	pending := uploadLifecycleTestRow(
		"object-1",
		UploadStatePending,
		ReservationStateReserved,
		0,
		now.Add(-time.Minute),
	)
	verifying := uploadLifecycleTestRow(
		"object-1",
		UploadStateVerifying,
		ReservationStateReserved,
		1,
		now,
	)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(pending)
	mock.ExpectExec("INSERT INTO object_storage_upload_events").
		WithArgs("tenant-1", "object-1", "pending", "transferred", "provider_transfer_reported", now).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO object_storage_upload_events").
		WithArgs("tenant-1", "object-1", "transferred", "verifying", "verification_started", now).
		WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectQuery("UPDATE object_storage_uploads").WillReturnRows(verifying)
	mock.ExpectCommit()

	upload, alreadyReady, err := repository.BeginVerification(
		context.Background(),
		"tenant-1",
		"object-1",
		now,
		2*time.Minute,
	)
	if err != nil {
		t.Fatalf("BeginVerification() error = %v", err)
	}
	if alreadyReady {
		t.Fatal("BeginVerification() alreadyReady = true, want false")
	}
	if upload.State != UploadStateVerifying || upload.AttemptCount != 1 {
		t.Fatalf("verification upload = %+v, want verifying attempt 1", upload)
	}

	// A concurrent replay cannot acquire the same live verification lease.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(
		uploadLifecycleTestRow(
			"object-1",
			UploadStateVerifying,
			ReservationStateReserved,
			1,
			now,
		),
	)
	mock.ExpectRollback()

	_, _, err = repository.BeginVerification(
		context.Background(),
		"tenant-1",
		"object-1",
		now.Add(time.Minute),
		2*time.Minute,
	)
	if !errors.Is(err, ErrUploadBusy) {
		t.Fatalf("concurrent BeginVerification() error = %v, want ErrUploadBusy", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresUploadLifecycleCommitVerifiedUploadPublishesAndAccountsExactlyOnce(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	repository := NewPostgresUploadLifecycle(sqlx.NewDb(rawDB, "sqlmock"))
	now := time.Date(2026, time.August, 11, 10, 10, 0, 0, time.UTC)
	checksum := "01f0f00d01f0f00d01f0f00d01f0f00d01f0f00d01f0f00d01f0f00d01f0f00d"

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(
		uploadLifecycleTestRow(
			"object-1",
			UploadStateVerifying,
			ReservationStateReserved,
			1,
			now.Add(-time.Second),
		),
	)
	mock.ExpectExec("INSERT INTO object_storage_objects").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE object_storage_usage").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO object_storage_upload_events").
		WithArgs("tenant-1", "object-1", "verifying", "ready", "verification_succeeded", now).
		WillReturnResult(sqlmock.NewResult(3, 1))
	mock.ExpectQuery("UPDATE object_storage_uploads").WillReturnRows(
		uploadLifecycleReadyTestRow("object-1", checksum, now),
	)
	mock.ExpectCommit()

	ready, alreadyReady, err := repository.CommitVerifiedUpload(
		context.Background(),
		"tenant-1",
		"object-1",
		1,
		VerifiedUpload{
			SizeBytes:      256,
			ChecksumSHA256: checksum,
			Now:            now,
		},
	)
	if err != nil {
		t.Fatalf("CommitVerifiedUpload() error = %v", err)
	}
	if alreadyReady {
		t.Fatal("CommitVerifiedUpload() alreadyReady = true, want false")
	}
	if ready.State != UploadStateReady || ready.ReservationState != ReservationStateCommitted {
		t.Fatalf("ready upload = %+v, want ready with committed reservation", ready)
	}

	// A replay returns the durable ready result without inserting or charging.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(
		uploadLifecycleReadyTestRow("object-1", checksum, now),
	)
	mock.ExpectCommit()

	replayed, alreadyReady, err := repository.CommitVerifiedUpload(
		context.Background(),
		"tenant-1",
		"object-1",
		1,
		VerifiedUpload{
			SizeBytes:      256,
			ChecksumSHA256: checksum,
			Now:            now.Add(time.Second),
		},
	)
	if err != nil {
		t.Fatalf("replayed CommitVerifiedUpload() error = %v", err)
	}
	if !alreadyReady || replayed.ActualChecksumSHA256 != checksum {
		t.Fatalf("replayed ready upload = %+v, alreadyReady=%v", replayed, alreadyReady)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresUploadLifecycleVerificationFailureStaysVisibleAndKeepsReservation(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	repository := NewPostgresUploadLifecycle(sqlx.NewDb(rawDB, "sqlmock"))
	now := time.Date(2026, time.August, 11, 10, 15, 0, 0, time.UTC)
	retryAt := now.Add(time.Minute)
	checksum := "02f0f00d02f0f00d02f0f00d02f0f00d02f0f00d02f0f00d02f0f00d02f0f00d"

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(
		uploadLifecycleTestRow(
			"object-1",
			UploadStateVerifying,
			ReservationStateReserved,
			1,
			now.Add(-time.Second),
		),
	)
	mock.ExpectExec("INSERT INTO object_storage_upload_events").
		WithArgs("tenant-1", "object-1", "verifying", "quarantined", "checksum_mismatch", now).
		WillReturnResult(sqlmock.NewResult(4, 1))
	mock.ExpectQuery("UPDATE object_storage_uploads").WillReturnRows(
		uploadLifecycleFailedTestRow(
			"object-1",
			UploadStateQuarantined,
			"checksum_mismatch",
			checksum,
			retryAt,
			now,
		),
	)
	mock.ExpectCommit()

	failed, err := repository.FailVerification(
		context.Background(),
		"tenant-1",
		"object-1",
		1,
		VerificationFailure{
			State:          UploadStateQuarantined,
			Code:           "checksum_mismatch",
			ActualSize:     256,
			ActualChecksum: checksum,
			RetryAt:        retryAt,
			Now:            now,
		},
	)
	if err != nil {
		t.Fatalf("FailVerification() error = %v", err)
	}
	if failed.State != UploadStateQuarantined || failed.LastErrorCode != "checksum_mismatch" {
		t.Fatalf("failed upload = %+v, want visible checksum quarantine", failed)
	}
	if failed.ReservationState != ReservationStateReserved {
		t.Fatalf("failed reservation state = %q, want reserved until cleanup", failed.ReservationState)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresUploadLifecycleDeletionReleasesCommittedUsageExactlyOnce(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	repository := NewPostgresUploadLifecycle(sqlx.NewDb(rawDB, "sqlmock"))
	now := time.Date(2026, time.August, 11, 10, 20, 0, 0, time.UTC)
	checksum := "03f0f00d03f0f00d03f0f00d03f0f00d03f0f00d03f0f00d03f0f00d03f0f00d"

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(
		uploadLifecycleReadyTestRow("object-1", checksum, now.Add(-time.Minute)),
	)
	mock.ExpectExec("INSERT INTO object_storage_upload_events").
		WithArgs("tenant-1", "object-1", "ready", "deleting", "deletion_started", now).
		WillReturnResult(sqlmock.NewResult(5, 1))
	mock.ExpectQuery("UPDATE object_storage_uploads").WillReturnRows(
		uploadLifecycleDeletionTestRow(
			"object-1",
			UploadStateDeleting,
			ReservationStateCommitted,
			checksum,
			now,
		),
	)
	mock.ExpectCommit()

	deleting, alreadyDeleted, err := repository.BeginDeletion(
		context.Background(),
		"tenant-1",
		"object-1",
		now,
	)
	if err != nil {
		t.Fatalf("BeginDeletion() error = %v", err)
	}
	if alreadyDeleted || deleting.State != UploadStateDeleting {
		t.Fatalf("BeginDeletion() = %+v, alreadyDeleted=%v", deleting, alreadyDeleted)
	}

	deletedAt := now.Add(time.Second)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(
		uploadLifecycleDeletionTestRow(
			"object-1",
			UploadStateDeleting,
			ReservationStateCommitted,
			checksum,
			now,
		),
	)
	mock.ExpectExec("UPDATE object_storage_objects").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE object_storage_usage").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO object_storage_upload_events").
		WithArgs("tenant-1", "object-1", "deleting", "deleted", "provider_delete_succeeded", deletedAt).
		WillReturnResult(sqlmock.NewResult(6, 1))
	mock.ExpectQuery("UPDATE object_storage_uploads").WillReturnRows(
		uploadLifecycleDeletionTestRow(
			"object-1",
			UploadStateDeleted,
			ReservationStateReleased,
			checksum,
			deletedAt,
		),
	)
	mock.ExpectCommit()

	deleted, alreadyDeleted, err := repository.CompleteDeletion(
		context.Background(),
		"tenant-1",
		"object-1",
		deletedAt,
	)
	if err != nil {
		t.Fatalf("CompleteDeletion() error = %v", err)
	}
	if alreadyDeleted || deleted.State != UploadStateDeleted || deleted.ReservationState != ReservationStateReleased {
		t.Fatalf("CompleteDeletion() = %+v, alreadyDeleted=%v", deleted, alreadyDeleted)
	}

	// A replay is successful without decrementing usage a second time.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(
		uploadLifecycleDeletionTestRow(
			"object-1",
			UploadStateDeleted,
			ReservationStateReleased,
			checksum,
			deletedAt,
		),
	)
	mock.ExpectCommit()

	_, alreadyDeleted, err = repository.CompleteDeletion(
		context.Background(),
		"tenant-1",
		"object-1",
		deletedAt.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("replayed CompleteDeletion() error = %v", err)
	}
	if !alreadyDeleted {
		t.Fatal("replayed CompleteDeletion() alreadyDeleted = false, want true")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresUploadLifecycleDeleteFailureRemainsDeletingForReconciliation(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	repository := NewPostgresUploadLifecycle(sqlx.NewDb(rawDB, "sqlmock"))
	now := time.Date(2026, time.August, 11, 10, 25, 0, 0, time.UTC)
	retryAt := now.Add(2 * time.Minute)
	checksum := "04f0f00d04f0f00d04f0f00d04f0f00d04f0f00d04f0f00d04f0f00d04f0f00d"

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(
		uploadLifecycleDeletionTestRow(
			"object-1",
			UploadStateDeleting,
			ReservationStateCommitted,
			checksum,
			now.Add(-time.Second),
		),
	)
	mock.ExpectExec("INSERT INTO object_storage_upload_events").
		WithArgs("tenant-1", "object-1", "deleting", "deleting", "provider_unavailable", now).
		WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectQuery("UPDATE object_storage_uploads").WillReturnRows(
		uploadLifecycleDeleteFailureTestRow("object-1", checksum, "provider_unavailable", retryAt, now),
	)
	mock.ExpectCommit()

	failed, err := repository.FailDeletion(
		context.Background(),
		"tenant-1",
		"object-1",
		"provider_unavailable",
		retryAt,
		now,
	)
	if err != nil {
		t.Fatalf("FailDeletion() error = %v", err)
	}
	if failed.State != UploadStateDeleting || failed.LastErrorCode != "provider_unavailable" {
		t.Fatalf("failed deletion = %+v, want visible deleting retry", failed)
	}
	if failed.ReservationState != ReservationStateCommitted {
		t.Fatalf("failed deletion accounting = %q, want committed until provider success", failed.ReservationState)
	}
	if !failed.NextAttemptAt.Valid || !failed.NextAttemptAt.Time.Equal(retryAt) {
		t.Fatalf("failed deletion retry = %v, want %v", failed.NextAttemptAt, retryAt)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresUploadLifecycleDeletionRetryWaitsUntilDue(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	repository := NewPostgresUploadLifecycle(sqlx.NewDb(rawDB, "sqlmock"))
	now := time.Date(2026, time.August, 11, 10, 27, 0, 0, time.UTC)
	retryAt := now.Add(time.Minute)
	checksum := "04f0f00d04f0f00d04f0f00d04f0f00d04f0f00d04f0f00d04f0f00d04f0f00d"

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(
		uploadLifecycleDeleteFailureTestRow("object-1", checksum, "provider_unavailable", retryAt, now.Add(-time.Minute)),
	)
	mock.ExpectRollback()

	_, _, err = repository.BeginDeletion(context.Background(), "tenant-1", "object-1", now)
	if !errors.Is(err, ErrUploadBusy) {
		t.Fatalf("BeginDeletion() error = %v, want ErrUploadBusy before retry time", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresUploadLifecycleStatusIncludesVisibleTransitionHistory(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	repository := NewPostgresUploadLifecycle(sqlx.NewDb(rawDB, "sqlmock"))
	now := time.Date(2026, time.August, 11, 10, 30, 0, 0, time.UTC)
	retryAt := now.Add(time.Minute)
	checksum := "05f0f00d05f0f00d05f0f00d05f0f00d05f0f00d05f0f00d05f0f00d05f0f00d"

	mock.ExpectQuery("SELECT .* FROM object_storage_uploads WHERE tenant_id").WillReturnRows(
		uploadLifecycleFailedTestRow(
			"object-1",
			UploadStateQuarantined,
			"checksum_mismatch",
			checksum,
			retryAt,
			now,
		),
	)
	mock.ExpectQuery("SELECT sequence, tenant_id, object_id, from_state, to_state, reason_code, created_at").
		WillReturnRows(
			sqlmock.NewRows(uploadTransitionColumns()).
				AddRow(int64(1), "tenant-1", "object-1", "", "pending", "initiated", now.Add(-2*time.Minute)).
				AddRow(int64(2), "tenant-1", "object-1", "verifying", "quarantined", "checksum_mismatch", now),
		)

	upload, transitions, err := repository.GetStatus(context.Background(), "tenant-1", "object-1")
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}
	if upload.State != UploadStateQuarantined || len(transitions) != 2 {
		t.Fatalf("GetStatus() = %+v, transitions=%+v", upload, transitions)
	}
	if transitions[1].ReasonCode != "checksum_mismatch" || transitions[1].ToState != UploadStateQuarantined {
		t.Fatalf("last transition = %+v, want visible checksum quarantine", transitions[1])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresUploadLifecycleInitiationFailureReleasesReservationExactlyOnce(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	repository := NewPostgresUploadLifecycle(sqlx.NewDb(rawDB, "sqlmock"))
	now := time.Date(2026, time.August, 11, 10, 35, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(
		uploadLifecycleTestRow(
			"object-1",
			UploadStatePending,
			ReservationStateReserved,
			0,
			now.Add(-time.Second),
		),
	)
	mock.ExpectExec("UPDATE object_storage_usage").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO object_storage_upload_events").
		WithArgs("tenant-1", "object-1", "pending", "failed", "presign_failed", now).
		WillReturnResult(sqlmock.NewResult(8, 1))
	mock.ExpectQuery("UPDATE object_storage_uploads").WillReturnRows(
		uploadLifecycleInitiationFailureTestRow("object-1", now),
	)
	mock.ExpectCommit()

	failed, alreadyReleased, err := repository.FailInitiation(
		context.Background(),
		"tenant-1",
		"object-1",
		"presign_failed",
		now,
	)
	if err != nil {
		t.Fatalf("FailInitiation() error = %v", err)
	}
	if alreadyReleased || failed.State != UploadStateFailed || failed.ReservationState != ReservationStateReleased {
		t.Fatalf("FailInitiation() = %+v, alreadyReleased=%v", failed, alreadyReleased)
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*FOR UPDATE").WillReturnRows(
		uploadLifecycleInitiationFailureTestRow("object-1", now),
	)
	mock.ExpectCommit()

	_, alreadyReleased, err = repository.FailInitiation(
		context.Background(),
		"tenant-1",
		"object-1",
		"presign_failed",
		now.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("replayed FailInitiation() error = %v", err)
	}
	if !alreadyReleased {
		t.Fatal("replayed FailInitiation() alreadyReleased = false, want true")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresUploadLifecycleListsBoundedDueReconciliationCandidates(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	repository := NewPostgresUploadLifecycle(sqlx.NewDb(rawDB, "sqlmock"))
	now := time.Date(2026, time.August, 11, 16, 40, 0, 0, time.UTC)
	lease := 2 * time.Minute

	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*state = 'deleting'.*LIMIT").
		WithArgs(now, now.Add(-lease), 2).
		WillReturnRows(
			uploadLifecycleTestRow("object-1", UploadStateFailed, ReservationStateReserved, 1, now.Add(-time.Minute)).
				AddRow(
					"object-2", "tenant-1", "config-1",
					"tenants/tenant-1/upload/object-2/report.txt", "report.txt", "text/plain",
					int64(256), "", int64(0), "", "upload", "", "", []byte(`{}`),
					"request-2", "fingerprint-2", string(UploadStateDeleting), string(ReservationStateReserved),
					"provider_delete_failed", int64(2), now.Add(-time.Minute), now.Add(-time.Second), "",
					now.Add(time.Hour), now.Add(-time.Hour), now,
				),
		)

	candidates, err := repository.ListReconcileCandidates(context.Background(), now, lease, 2)
	if err != nil {
		t.Fatalf("ListReconcileCandidates() error = %v", err)
	}
	if len(candidates) != 2 || candidates[0].ObjectID != "object-1" || candidates[1].ObjectID != "object-2" {
		t.Fatalf("candidates = %+v, want bounded due rows", candidates)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresUploadLifecycleImportsReadyProviderObjectExactlyOnce(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	repository := NewPostgresUploadLifecycle(sqlx.NewDb(rawDB, "sqlmock"))
	now := time.Date(2026, time.August, 11, 16, 45, 0, 0, time.UTC)
	params := ImportReadyUploadParams{
		ObjectID:       "object-1",
		TenantID:       "tenant-1",
		ConfigID:       "config-1",
		Key:            "tenants/tenant-1/upload/existing.txt",
		Filename:       "existing.txt",
		ContentType:    "text/plain",
		SizeBytes:      12,
		Purpose:        "upload",
		IdempotencyKey: "import:config-1:abc",
		ImportedAt:     now,
	}
	readyRow := func() *sqlmock.Rows {
		return sqlmock.NewRows(uploadLifecycleColumns()).AddRow(
			params.ObjectID, params.TenantID, params.ConfigID, params.Key,
			params.Filename, params.ContentType, params.SizeBytes, "",
			params.SizeBytes, "", params.Purpose, "", "", []byte(`{}`),
			params.IdempotencyKey, params.ConfigID+"\x00"+params.Key,
			string(UploadStateReady), string(ReservationStateCommitted), "", int64(0),
			nil, nil, "", now, now, now,
		)
	}

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*config_id = \\$2 AND key = \\$3.*state <> 'deleted'").
		WillReturnRows(sqlmock.NewRows(uploadLifecycleColumns()))
	mock.ExpectQuery("INSERT INTO object_storage_uploads").WillReturnRows(
		readyRow(),
	)
	mock.ExpectExec("INSERT INTO object_storage_objects").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO object_storage_usage").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO object_storage_upload_events").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	first, created, err := repository.ImportReady(context.Background(), params)
	if err != nil {
		t.Fatalf("ImportReady() error = %v", err)
	}
	if !created || first.State != UploadStateReady || first.ReservationState != ReservationStateCommitted {
		t.Fatalf("ImportReady() = %+v created=%v, want ready committed", first, created)
	}

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*config_id = \\$2 AND key = \\$3.*state <> 'deleted'").WillReturnRows(
		readyRow(),
	)
	mock.ExpectCommit()

	_, created, err = repository.ImportReady(context.Background(), params)
	if err != nil {
		t.Fatalf("replayed ImportReady() error = %v", err)
	}
	if created {
		t.Fatal("replayed ImportReady() created = true, want false")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresUploadLifecycleImportReusesExistingProviderKeyAcrossConcurrentScans(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	repository := NewPostgresUploadLifecycle(sqlx.NewDb(rawDB, "sqlmock"))
	now := time.Date(2026, time.August, 11, 16, 47, 0, 0, time.UTC)
	params := ImportReadyUploadParams{
		ObjectID:       "discarded-object",
		TenantID:       "tenant-1",
		ConfigID:       "config-1",
		Key:            "tenants/tenant-1/upload/existing.txt",
		Filename:       "existing.txt",
		ContentType:    "text/plain",
		SizeBytes:      12,
		Purpose:        "upload",
		IdempotencyKey: "import:new-scan",
		ImportedAt:     now,
	}
	existing := sqlmock.NewRows(uploadLifecycleColumns()).AddRow(
		"existing-object", params.TenantID, params.ConfigID, params.Key,
		params.Filename, params.ContentType, params.SizeBytes, "", params.SizeBytes, "",
		params.Purpose, "", "", []byte(`{}`), "import:first-scan",
		params.ConfigID+"\x00"+params.Key, string(UploadStateReady), string(ReservationStateCommitted),
		"", int64(0), nil, nil, "", now, now, now,
	)

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT .* FROM object_storage_uploads.*config_id = \\$2 AND key = \\$3.*state <> 'deleted'").WillReturnRows(existing)
	mock.ExpectCommit()

	upload, created, err := repository.ImportReady(context.Background(), params)
	if err != nil {
		t.Fatalf("ImportReady() concurrent key replay error = %v", err)
	}
	if created || upload.ObjectID != "existing-object" {
		t.Fatalf("ImportReady() = %+v created=%v, want existing provider object", upload, created)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func uploadLifecycleTestRow(objectID string, state UploadState, reservation ReservationState, attempt int64, updatedAt time.Time) *sqlmock.Rows {
	return sqlmock.NewRows(uploadLifecycleColumns()).AddRow(
		objectID,
		"tenant-1",
		"config-1",
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
		string(state),
		string(reservation),
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

func uploadLifecycleReadyTestRow(objectID, checksum string, updatedAt time.Time) *sqlmock.Rows {
	return sqlmock.NewRows(uploadLifecycleColumns()).AddRow(
		objectID,
		"tenant-1",
		"config-1",
		"tenants/tenant-1/upload/"+objectID+"/report.txt",
		"report.txt",
		"text/plain",
		int64(256),
		"",
		int64(256),
		checksum,
		"upload",
		"",
		"",
		[]byte(`{}`),
		"request-1",
		"fingerprint-1",
		string(UploadStateReady),
		string(ReservationStateCommitted),
		"",
		int64(1),
		nil,
		nil,
		"",
		updatedAt.Add(time.Hour),
		updatedAt.Add(-time.Hour),
		updatedAt,
	)
}

func uploadLifecycleFailedTestRow(objectID string, state UploadState, code, checksum string, retryAt, updatedAt time.Time) *sqlmock.Rows {
	return sqlmock.NewRows(uploadLifecycleColumns()).AddRow(
		objectID,
		"tenant-1",
		"config-1",
		"tenants/tenant-1/upload/"+objectID+"/report.txt",
		"report.txt",
		"text/plain",
		int64(256),
		"",
		int64(256),
		checksum,
		"upload",
		"",
		"",
		[]byte(`{}`),
		"request-1",
		"fingerprint-1",
		string(state),
		string(ReservationStateReserved),
		code,
		int64(1),
		updatedAt,
		retryAt,
		"",
		updatedAt.Add(time.Hour),
		updatedAt.Add(-time.Hour),
		updatedAt,
	)
}

func uploadLifecycleDeletionTestRow(objectID string, state UploadState, reservation ReservationState, checksum string, updatedAt time.Time) *sqlmock.Rows {
	return sqlmock.NewRows(uploadLifecycleColumns()).AddRow(
		objectID,
		"tenant-1",
		"config-1",
		"tenants/tenant-1/upload/"+objectID+"/report.txt",
		"report.txt",
		"text/plain",
		int64(256),
		"",
		int64(256),
		checksum,
		"upload",
		"",
		"",
		[]byte(`{}`),
		"request-1",
		"fingerprint-1",
		string(state),
		string(reservation),
		"",
		int64(1),
		nil,
		nil,
		"",
		updatedAt.Add(time.Hour),
		updatedAt.Add(-time.Hour),
		updatedAt,
	)
}

func uploadLifecycleDeleteFailureTestRow(objectID, checksum, code string, retryAt, updatedAt time.Time) *sqlmock.Rows {
	return sqlmock.NewRows(uploadLifecycleColumns()).AddRow(
		objectID,
		"tenant-1",
		"config-1",
		"tenants/tenant-1/upload/"+objectID+"/report.txt",
		"report.txt",
		"text/plain",
		int64(256),
		"",
		int64(256),
		checksum,
		"upload",
		"",
		"",
		[]byte(`{}`),
		"request-1",
		"fingerprint-1",
		string(UploadStateDeleting),
		string(ReservationStateCommitted),
		code,
		int64(2),
		updatedAt,
		retryAt,
		"",
		updatedAt.Add(time.Hour),
		updatedAt.Add(-time.Hour),
		updatedAt,
	)
}

func uploadLifecycleInitiationFailureTestRow(objectID string, updatedAt time.Time) *sqlmock.Rows {
	return sqlmock.NewRows(uploadLifecycleColumns()).AddRow(
		objectID,
		"tenant-1",
		"config-1",
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
		string(UploadStateFailed),
		string(ReservationStateReleased),
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
