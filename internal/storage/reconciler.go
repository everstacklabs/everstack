package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/everstacklabs/everstack/internal/storageauth"
)

// UploadReconcileRepository is the durable state boundary used by the worker.
// Provider work always happens outside these short database transactions.
type UploadReconcileRepository interface {
	ListReconcileCandidates(ctx context.Context, now time.Time, verificationLease time.Duration, limit int) ([]Upload, error)
	BeginDeletion(ctx context.Context, tenantID, objectID string, now time.Time) (*Upload, bool, error)
	CompleteDeletion(ctx context.Context, tenantID, objectID string, now time.Time) (*Upload, bool, error)
	FailDeletion(ctx context.Context, tenantID, objectID, code string, retryAt, now time.Time) (*Upload, error)
}

// UploadReconcileExecutor owns provider I/O. VerifyUpload must durably record
// either ready or a visible verification failure before returning.
type UploadReconcileExecutor interface {
	VerifyUpload(ctx context.Context, upload *Upload, now time.Time) error
	DeleteUpload(ctx context.Context, upload *Upload) error
}

// UploadReconciler converges retryable and abandoned uploads in bounded
// batches. Lifecycle transactions remain the exact-once accounting guard, so
// overlapping workers may repeat provider I/O but cannot double charge.
type UploadReconciler struct {
	repository        UploadReconcileRepository
	executor          UploadReconcileExecutor
	Interval          time.Duration
	VerificationLease time.Duration
	BatchSize         int
}

func NewUploadReconciler(repository UploadReconcileRepository, executor UploadReconcileExecutor) *UploadReconciler {
	return &UploadReconciler{
		repository:        repository,
		executor:          executor,
		Interval:          time.Minute,
		VerificationLease: 2 * time.Minute,
		BatchSize:         100,
	}
}

// Run executes one startup pass and then continues until the process context
// is cancelled.
func (r *UploadReconciler) Run(ctx context.Context) error {
	if r == nil || r.repository == nil || r.executor == nil {
		return errors.New("storage upload reconciler is not configured")
	}
	interval := r.Interval
	if interval <= 0 {
		interval = time.Minute
	}

	r.runAndLog(ctx, time.Now().UTC())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			r.runAndLog(ctx, now.UTC())
		}
	}
}

func (r *UploadReconciler) runAndLog(ctx context.Context, now time.Time) {
	attempted, err := r.RunOnce(ctx, now)
	if err != nil {
		slog.Warn("storage upload reconciliation completed with retryable failures", "attempted", attempted, "error", err)
	}
}

// RunOnce processes one bounded candidate batch. Each candidate receives a
// tenant-bound system principal before any provider resolver or lifecycle
// operation runs.
func (r *UploadReconciler) RunOnce(ctx context.Context, now time.Time) (int, error) {
	if r == nil || r.repository == nil || r.executor == nil {
		return 0, errors.New("storage upload reconciler is not configured")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	lease := r.VerificationLease
	if lease <= 0 {
		lease = 2 * time.Minute
	}
	limit := r.BatchSize
	if limit <= 0 || limit > 1_000 {
		limit = 100
	}

	candidates, err := r.repository.ListReconcileCandidates(ctx, now, lease, limit)
	if err != nil {
		return 0, fmt.Errorf("list storage upload reconciliation candidates: %w", err)
	}

	var failures []error
	for index := range candidates {
		upload := &candidates[index]
		rowCtx := storageauth.WithSystemPrincipal(ctx, upload.TenantID)

		if shouldRetryUploadVerification(upload, now) {
			if err := r.executor.VerifyUpload(rowCtx, upload, now); err != nil && !errors.Is(err, ErrUploadBusy) {
				failures = append(failures, fmt.Errorf("verify upload %s/%s: %w", upload.TenantID, upload.ObjectID, err))
			}
			continue
		}
		if !shouldReconcileUploadDeletion(upload, now) {
			continue
		}
		if err := r.reconcileDeletion(rowCtx, upload, now); err != nil {
			failures = append(failures, err)
		}
	}
	return len(candidates), errors.Join(failures...)
}

func shouldRetryUploadVerification(upload *Upload, now time.Time) bool {
	if upload == nil || upload.ReservationState != ReservationStateReserved || !upload.ExpiresAt.After(now) {
		return false
	}
	return upload.State == UploadStateFailed || upload.State == UploadStateVerifying
}

func shouldReconcileUploadDeletion(upload *Upload, now time.Time) bool {
	if upload == nil || upload.State == UploadStateReady || upload.State == UploadStateDeleted {
		return false
	}
	if upload.State == UploadStateDeleting {
		return true
	}
	return !upload.ExpiresAt.After(now)
}

func (r *UploadReconciler) reconcileDeletion(ctx context.Context, candidate *Upload, now time.Time) error {
	upload, alreadyDeleted, err := r.repository.BeginDeletion(ctx, candidate.TenantID, candidate.ObjectID, now)
	if err != nil {
		return fmt.Errorf("begin reconciled upload deletion %s/%s: %w", candidate.TenantID, candidate.ObjectID, err)
	}
	if alreadyDeleted {
		return nil
	}

	if err := r.executor.DeleteUpload(ctx, upload); err != nil {
		code := "provider_delete_failed"
		var operationErr *OperationError
		if errors.As(err, &operationErr) && operationErr.Code != "" {
			code = string(operationErr.Code)
		}
		retryAt := now.Add(uploadDeleteRetryDelay(upload.AttemptCount))
		if _, failureErr := r.repository.FailDeletion(
			ctx,
			upload.TenantID,
			upload.ObjectID,
			code,
			retryAt,
			now,
		); failureErr != nil {
			return fmt.Errorf("record reconciled upload deletion failure %s/%s: %w", upload.TenantID, upload.ObjectID, failureErr)
		}
		return fmt.Errorf("delete reconciled provider upload %s/%s: %w", upload.TenantID, upload.ObjectID, err)
	}

	if _, _, err := r.repository.CompleteDeletion(ctx, upload.TenantID, upload.ObjectID, now); err != nil {
		return fmt.Errorf("complete reconciled upload deletion %s/%s: %w", upload.TenantID, upload.ObjectID, err)
	}
	return nil
}

func uploadDeleteRetryDelay(attempt int64) time.Duration {
	delay := time.Minute
	for step := int64(0); step < attempt && step < 4; step++ {
		delay *= 2
	}
	return delay
}
