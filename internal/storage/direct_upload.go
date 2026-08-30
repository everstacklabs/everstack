package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/everstacklabs/everstack/internal/storageauth"
)

// DirectUploader moves trusted server-side upload callers through the same
// lifecycle as presigned clients. The provider write is followed by an
// independent provider read before ready publication.
type DirectUploader struct {
	store     ObjectStore
	lifecycle *PostgresUploadLifecycle
	bucket    string
}

func NewDirectUploader(store ObjectStore, lifecycle *PostgresUploadLifecycle, bucket string) *DirectUploader {
	return &DirectUploader{store: store, lifecycle: lifecycle, bucket: bucket}
}

// Upload writes and verifies one logical object. Its InitiateUploadParams must
// contain the final provider key, idempotency identity, and resolved quota.
func (u *DirectUploader) Upload(
	ctx context.Context,
	params InitiateUploadParams,
	body io.Reader,
) (*Upload, string, error) {
	if u == nil || u.store == nil || u.lifecycle == nil || body == nil {
		return nil, "", errors.New("direct storage uploader is not configured")
	}
	if _, err := storageauth.AuthorizeTenant(ctx, storageauth.ActionUploadInternal, params.TenantID); err != nil {
		return nil, "", err
	}

	upload, _, err := u.lifecycle.Initiate(ctx, params)
	if err != nil {
		return nil, "", err
	}
	if upload.State == UploadStateReady {
		return upload, "", nil
	}
	if upload.ReservationState != ReservationStateReserved {
		return nil, "", fmt.Errorf("%w: direct upload has no reservation", ErrInvalidUploadState)
	}

	etag, err := u.store.Put(ctx, u.bucket, upload.Key, upload.ContentType, body)
	if err != nil {
		u.cleanupFailedWrite(ctx, upload, time.Now().UTC())
		return nil, "", fmt.Errorf("direct provider write failed: %w", err)
	}

	ready, err := u.verify(ctx, upload, params.Metadata, time.Now().UTC())
	if err != nil {
		return nil, "", err
	}
	return ready, etag, nil
}

func (u *DirectUploader) verify(ctx context.Context, upload *Upload, metadata []byte, now time.Time) (*Upload, error) {
	verifying, alreadyReady, err := u.lifecycle.BeginVerification(ctx, upload.TenantID, upload.ObjectID, now, 2*time.Minute)
	if err != nil {
		return nil, err
	}
	if alreadyReady {
		return verifying, nil
	}

	blobStore, err := RequireBlobPlane(u.store)
	if err != nil {
		u.failVerification(ctx, verifying, UploadStateFailed, "blob_plane_unsupported", 0, "", now)
		return nil, err
	}
	reader, err := blobStore.Get(ctx, u.bucket, verifying.Key)
	if err != nil {
		code := "provider_unavailable"
		if IsCode(err, ErrorNotFound) {
			code = "object_not_found"
		}
		u.failVerification(ctx, verifying, UploadStateFailed, code, 0, "", now)
		return nil, err
	}
	if reader == nil || reader.Body == nil {
		u.failVerification(ctx, verifying, UploadStateFailed, "provider_invalid_response", 0, "", now)
		return nil, errors.New("storage provider returned an invalid object")
	}
	defer reader.Body.Close()

	hasher := sha256.New()
	readLimit := verifying.ExpectedSizeBytes + 1
	if verifying.ExpectedSizeBytes == int64(^uint64(0)>>1) {
		readLimit = verifying.ExpectedSizeBytes
	}
	actualSize, readErr := io.Copy(hasher, io.LimitReader(reader.Body, readLimit))
	actualChecksum := hex.EncodeToString(hasher.Sum(nil))
	if readErr != nil {
		u.failVerification(ctx, verifying, UploadStateFailed, "provider_read_failed", actualSize, actualChecksum, now)
		return nil, fmt.Errorf("read direct provider object: %w", readErr)
	}
	if actualSize != verifying.ExpectedSizeBytes || reader.SizeBytes != verifying.ExpectedSizeBytes {
		u.failVerification(ctx, verifying, UploadStateQuarantined, "size_mismatch", actualSize, actualChecksum, now)
		return nil, errors.New("direct provider object size does not match reservation")
	}
	if verifying.ExpectedChecksumSHA256 != "" && actualChecksum != verifying.ExpectedChecksumSHA256 {
		u.failVerification(ctx, verifying, UploadStateQuarantined, "checksum_mismatch", actualSize, actualChecksum, now)
		return nil, errors.New("direct provider object checksum does not match expectation")
	}

	ready, _, err := u.lifecycle.CommitVerifiedUpload(ctx, verifying.TenantID, verifying.ObjectID, verifying.AttemptCount, VerifiedUpload{
		SizeBytes:      actualSize,
		ChecksumSHA256: actualChecksum,
		Metadata:       metadata,
		Now:            time.Now().UTC(),
	})
	return ready, err
}

func (u *DirectUploader) failVerification(
	ctx context.Context,
	upload *Upload,
	state UploadState,
	code string,
	actualSize int64,
	actualChecksum string,
	now time.Time,
) {
	_, _ = u.lifecycle.FailVerification(ctx, upload.TenantID, upload.ObjectID, upload.AttemptCount, VerificationFailure{
		State:          state,
		Code:           code,
		ActualSize:     actualSize,
		ActualChecksum: actualChecksum,
		RetryAt:        now.Add(time.Minute),
		Now:            now,
	})
}

func (u *DirectUploader) cleanupFailedWrite(ctx context.Context, upload *Upload, now time.Time) {
	deleting, alreadyDeleted, err := u.lifecycle.BeginDeletion(ctx, upload.TenantID, upload.ObjectID, now)
	if err != nil || alreadyDeleted {
		return
	}
	if err := u.store.Delete(ctx, u.bucket, deleting.Key); err != nil && !IsCode(err, ErrorNotFound) {
		_, _ = u.lifecycle.FailDeletion(
			ctx,
			deleting.TenantID,
			deleting.ObjectID,
			"provider_delete_failed",
			now.Add(time.Minute),
			now,
		)
		return
	}
	_, _, _ = u.lifecycle.CompleteDeletion(ctx, deleting.TenantID, deleting.ObjectID, now)
}
