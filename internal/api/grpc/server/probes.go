package server

import (
	"context"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/everstacklabs/everstack/internal/lib/mferrors"
	// "github.com/everstacklabs/everstack/internal/telemetry/tracing"
)

type ValidationFunction func(ctx context.Context) error

type Validator struct {
	validations map[string]ValidationFunction
}

func NewValidator(validations map[string]ValidationFunction) *Validator {
	return &Validator{validations: validations}
}

func (v *Validator) Healthz(_ context.Context, e *emptypb.Empty) (*emptypb.Empty, error) {
	return e, nil
}

func (v *Validator) Ready(ctx context.Context, e *emptypb.Empty) (*emptypb.Empty, error) {
	if len(validate(ctx, v.validations)) == 0 {
		return e, nil
	}
	return nil, mferrors.ThrowInternal(nil, "API-PROBE-NOT-READY", "not ready")
}

func (v *Validator) Validate(ctx context.Context, _ *emptypb.Empty) (*structpb.Struct, error) {
	return structpb.NewStruct(validate(ctx, v.validations))
}

func validate(ctx context.Context, validations map[string]ValidationFunction) map[string]any {
	errors := make(map[string]any)
	for id, validation := range validations {
		if err := validation(ctx); err != nil {
			// logger.Log("API-vf823").WithError(err).WithField("traceID", tracing.TraceIDFromCtx(ctx)).Error("validation failed")
			errors[id] = err
		}
	}
	return errors
}
