package storage

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/storageauth"
)

type reconcileRepositoryFake struct {
	candidates       []Upload
	beginDeleteCalls []string
	completed        []string
	failed           map[string]string
	retryAt          map[string]time.Time
}

func (f *reconcileRepositoryFake) ListReconcileCandidates(context.Context, time.Time, time.Duration, int) ([]Upload, error) {
	return append([]Upload(nil), f.candidates...), nil
}

func (f *reconcileRepositoryFake) BeginDeletion(_ context.Context, tenantID, objectID string, now time.Time) (*Upload, bool, error) {
	f.beginDeleteCalls = append(f.beginDeleteCalls, tenantID+"/"+objectID)
	for i := range f.candidates {
		if f.candidates[i].TenantID == tenantID && f.candidates[i].ObjectID == objectID {
			if f.candidates[i].State == UploadStateDeleted {
				return &f.candidates[i], true, nil
			}
			f.candidates[i].State = UploadStateDeleting
			f.candidates[i].UpdatedAt = now
			return &f.candidates[i], false, nil
		}
	}
	return nil, false, ErrUploadNotFound
}

func (f *reconcileRepositoryFake) CompleteDeletion(_ context.Context, tenantID, objectID string, _ time.Time) (*Upload, bool, error) {
	f.completed = append(f.completed, tenantID+"/"+objectID)
	return &Upload{TenantID: tenantID, ObjectID: objectID, State: UploadStateDeleted}, false, nil
}

func (f *reconcileRepositoryFake) FailDeletion(_ context.Context, tenantID, objectID, code string, retryAt, _ time.Time) (*Upload, error) {
	if f.failed == nil {
		f.failed = make(map[string]string)
	}
	if f.retryAt == nil {
		f.retryAt = make(map[string]time.Time)
	}
	identity := tenantID + "/" + objectID
	f.failed[identity] = code
	f.retryAt[identity] = retryAt
	return &Upload{TenantID: tenantID, ObjectID: objectID, State: UploadStateDeleting}, nil
}

type reconcileExecutorFake struct {
	verified   []string
	deleted    []string
	deleteFail map[string]error
}

func (f *reconcileExecutorFake) VerifyUpload(ctx context.Context, upload *Upload, _ time.Time) error {
	if _, err := storageauth.AuthorizeTenant(ctx, storageauth.ActionAdminReconcile, upload.TenantID); err != nil {
		return err
	}
	f.verified = append(f.verified, upload.TenantID+"/"+upload.ObjectID)
	return nil
}

func (f *reconcileExecutorFake) DeleteUpload(ctx context.Context, upload *Upload) error {
	if _, err := storageauth.AuthorizeTenant(ctx, storageauth.ActionAdminReconcile, upload.TenantID); err != nil {
		return err
	}
	identity := upload.TenantID + "/" + upload.ObjectID
	f.deleted = append(f.deleted, identity)
	return f.deleteFail[identity]
}

func TestUploadReconcilerRetriesVerificationAndCleansExpiredUploadsWithoutEarlyAccountingRelease(t *testing.T) {
	now := time.Date(2026, time.August, 11, 16, 30, 0, 0, time.UTC)
	repository := &reconcileRepositoryFake{candidates: []Upload{
		{
			TenantID:         "tenant-1",
			ObjectID:         "verify-me",
			State:            UploadStateFailed,
			ReservationState: ReservationStateReserved,
			NextAttemptAt:    sqlNullTime(now.Add(-time.Second)),
			ExpiresAt:        now.Add(time.Hour),
		},
		{
			TenantID:         "tenant-2",
			ObjectID:         "expired-multipart",
			State:            UploadStatePending,
			ReservationState: ReservationStateReserved,
			ExpiresAt:        now.Add(-time.Second),
		},
		{
			TenantID:         "tenant-3",
			ObjectID:         "retry-delete",
			State:            UploadStateDeleting,
			ReservationState: ReservationStateCommitted,
			NextAttemptAt:    sqlNullTime(now.Add(-time.Second)),
			ExpiresAt:        now.Add(-time.Hour),
		},
	}}
	executor := &reconcileExecutorFake{deleteFail: map[string]error{
		"tenant-3/retry-delete": NewOperationError("delete", ErrorUnavailable, true, "SlowDown", 503),
	}}
	reconciler := NewUploadReconciler(repository, executor)
	reconciler.BatchSize = 10

	attempted, err := reconciler.RunOnce(context.Background(), now)
	if attempted != 3 {
		t.Fatalf("RunOnce() attempted = %d, want 3", attempted)
	}
	if err == nil {
		t.Fatal("RunOnce() error = nil, want the provider delete failure reported")
	}
	if got := executor.verified; len(got) != 1 || got[0] != "tenant-1/verify-me" {
		t.Fatalf("verified = %v, want failed upload retried", got)
	}
	if got := executor.deleted; len(got) != 2 || got[0] != "tenant-2/expired-multipart" || got[1] != "tenant-3/retry-delete" {
		t.Fatalf("deleted = %v, want expired and deleting uploads attempted", got)
	}
	if got := repository.completed; len(got) != 1 || got[0] != "tenant-2/expired-multipart" {
		t.Fatalf("completed = %v, want only provider-confirmed deletion accounted", got)
	}
	if repository.failed["tenant-3/retry-delete"] != "unavailable" {
		t.Fatalf("delete failure code = %q, want unavailable", repository.failed["tenant-3/retry-delete"])
	}
	if !repository.retryAt["tenant-3/retry-delete"].After(now) {
		t.Fatalf("retry time = %v, want visible future retry", repository.retryAt["tenant-3/retry-delete"])
	}
}

func sqlNullTime(value time.Time) sql.NullTime {
	return sql.NullTime{Time: value, Valid: true}
}
