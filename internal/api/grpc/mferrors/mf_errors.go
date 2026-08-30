package mferrors

import (
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/protoadapt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/everstacklabs/everstack/internal/lib/mferrors"
	"github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/message"
)

func MFToConnectError(err error) error {
	if err == nil {
		return nil
	}

	connectError := new(connect.Error)
	if errors.As(err, &connectError) {
		return connectError
	}

	code, key, id, ok := ExtractMFError(err)
	if !ok {
		return status.Convert(err).Err()
	}
	msg := key
	msg += " (" + id + ")"

	errInfo := getErrorInfo(id, key, err)

	cErr := connect.NewError(connect.Code(code), errors.New(msg))
	if detail, detailErr := connect.NewErrorDetail(errInfo.(proto.Message)); detailErr == nil {
		cErr.AddDetail(detail)
	}
	return cErr
}

func ExtractMFError(err error) (c codes.Code, msg, id string, ok bool) {
	if err == nil {
		return codes.OK, "", "", false
	}
	connErr := new(pgconn.ConnectError)
	if ok := errors.As(err, &connErr); ok {
		return codes.Internal, "db connection error", "", true
	}
	mfErr := new(mferrors.EverstackError)
	if ok := errors.As(err, &mfErr); !ok {
		return codes.Unknown, err.Error(), "", false
	}
	switch {
	case mferrors.IsErrorAlreadyExists(err):
		return codes.AlreadyExists, mfErr.GetMessage(), mfErr.GetID(), true
	case mferrors.IsDeadlineExceeded(err):
		return codes.DeadlineExceeded, mfErr.GetMessage(), mfErr.GetID(), true
	case mferrors.IsInternal(err):
		return codes.Internal, mfErr.GetMessage(), mfErr.GetID(), true
	case mferrors.IsInvalidArgument(err):
		return codes.InvalidArgument, mfErr.GetMessage(), mfErr.GetID(), true
	case mferrors.IsNotFound(err):
		return codes.NotFound, mfErr.GetMessage(), mfErr.GetID(), true
	case mferrors.IsPermissionDenied(err):
		return codes.PermissionDenied, mfErr.GetMessage(), mfErr.GetID(), true
	case mferrors.IsPreconditionFailed(err):
		return codes.FailedPrecondition, mfErr.GetMessage(), mfErr.GetID(), true
	case mferrors.IsUnauthenticated(err):
		return codes.Unauthenticated, mfErr.GetMessage(), mfErr.GetID(), true
	case mferrors.IsUnavailable(err):
		return codes.Unavailable, mfErr.GetMessage(), mfErr.GetID(), true
	case mferrors.IsUnimplemented(err):
		return codes.Unimplemented, mfErr.GetMessage(), mfErr.GetID(), true
	case mferrors.IsResourceExhausted(err):
		return codes.ResourceExhausted, mfErr.GetMessage(), mfErr.GetID(), true
	default:
		return codes.Unknown, err.Error(), "", false
	}
}

func getErrorInfo(id, key string, err error) protoadapt.MessageV1 {
	var errorInfo protoadapt.MessageV1

	if mferrors.IsEverstackError(err) {
		return &message.ErrorDetail{Id: id, Message: key}
	}

	errorInfo = &message.ErrorDetail{Id: id, Message: key}

	return errorInfo
}
