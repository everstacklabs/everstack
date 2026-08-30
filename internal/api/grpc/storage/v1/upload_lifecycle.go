package v1

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	storagepkg "github.com/everstacklabs/everstack/internal/storage"
	"github.com/everstacklabs/everstack/internal/storageauth"
	storagepb "github.com/everstacklabs/everstack/pkg/grpc/everstack/storage/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) GetUploadStatus(
	ctx context.Context,
	req *connect.Request[storagepb.GetUploadStatusRequest],
) (*connect.Response[storagepb.GetUploadStatusResponse], error) {
	tenantID, err := s.authorizeStorage(ctx, storageauth.ActionUploadRead)
	if err != nil {
		return nil, err
	}
	if req.Msg.GetObjectId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("object_id is required"))
	}
	if s.uploadLifecycle == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("upload lifecycle is not configured"))
	}

	upload, transitions, err := s.uploadLifecycle.GetStatus(ctx, tenantID, req.Msg.GetObjectId())
	if errors.Is(err, storagepkg.ErrUploadNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("upload not found"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to read upload status"))
	}

	return connect.NewResponse(&storagepb.GetUploadStatusResponse{
		Upload: uploadLifecycleToProto(upload, transitions),
	}), nil
}

func uploadLifecycleToProto(upload *storagepkg.Upload, transitions []storagepkg.UploadTransition) *storagepb.StorageUpload {
	result := &storagepb.StorageUpload{
		ObjectId:               upload.ObjectID,
		TenantId:               upload.TenantID,
		ConfigId:               upload.ConfigID,
		Key:                    upload.Key,
		Filename:               upload.Filename,
		ContentType:            upload.ContentType,
		ExpectedSizeBytes:      upload.ExpectedSizeBytes,
		ExpectedChecksumSha256: upload.ExpectedChecksumSHA256,
		ActualSizeBytes:        upload.ActualSizeBytes,
		ActualChecksumSha256:   upload.ActualChecksumSHA256,
		Purpose:                stringToPurpose(upload.Purpose),
		ReferenceId:            upload.ReferenceID,
		ReferenceType:          upload.ReferenceType,
		State:                  uploadStateToProto(upload.State),
		ReservationState:       string(upload.ReservationState),
		IdempotencyKey:         upload.IdempotencyKey,
		LastErrorCode:          upload.LastErrorCode,
		AttemptCount:           upload.AttemptCount,
		ExpiresAt:              timestamppb.New(upload.ExpiresAt),
		CreatedAt:              timestamppb.New(upload.CreatedAt),
		UpdatedAt:              timestamppb.New(upload.UpdatedAt),
	}
	if upload.LastErrorAt.Valid {
		result.LastErrorAt = timestamppb.New(upload.LastErrorAt.Time)
	}
	if upload.NextAttemptAt.Valid {
		result.NextAttemptAt = timestamppb.New(upload.NextAttemptAt.Time)
	}
	for _, transition := range transitions {
		result.Transitions = append(result.Transitions, &storagepb.UploadTransition{
			Sequence:   transition.Sequence,
			FromState:  uploadStateToProto(transition.FromState),
			ToState:    uploadStateToProto(transition.ToState),
			ReasonCode: transition.ReasonCode,
			CreatedAt:  timestamppb.New(transition.CreatedAt),
		})
	}
	return result
}

func uploadStateToProto(state storagepkg.UploadState) storagepb.UploadState {
	switch state {
	case storagepkg.UploadStatePending:
		return storagepb.UploadState_UPLOAD_STATE_PENDING
	case storagepkg.UploadStateTransferred:
		return storagepb.UploadState_UPLOAD_STATE_TRANSFERRED
	case storagepkg.UploadStateVerifying:
		return storagepb.UploadState_UPLOAD_STATE_VERIFYING
	case storagepkg.UploadStateReady:
		return storagepb.UploadState_UPLOAD_STATE_READY
	case storagepkg.UploadStateFailed:
		return storagepb.UploadState_UPLOAD_STATE_FAILED
	case storagepkg.UploadStateQuarantined:
		return storagepb.UploadState_UPLOAD_STATE_QUARANTINED
	case storagepkg.UploadStateDeleting:
		return storagepb.UploadState_UPLOAD_STATE_DELETING
	case storagepkg.UploadStateDeleted:
		return storagepb.UploadState_UPLOAD_STATE_DELETED
	default:
		return storagepb.UploadState_UPLOAD_STATE_UNSPECIFIED
	}
}
