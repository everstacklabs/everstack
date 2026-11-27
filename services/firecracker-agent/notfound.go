package main

// Error-code mapping interceptors.
//
// The FirecrackerBackend reports a missing VM as a plain
// fmt.Errorf("VM not found for sandbox %s", id), which gRPC ships as
// codes.Unknown. The gateway needs to tell "this VM is gone, stop
// retrying" apart from "the agent hiccuped, retry", and an Unknown
// code forces it into substring matching. These interceptors map the
// message onto codes.NotFound at the single point where every RPC
// leaves the agent, so the gateway can branch on the typed code while
// older message-only matching keeps working (the text is preserved).

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// isVMNotFoundMessage matches the backend's "VM not found for sandbox
// <id>" errors, tolerating the lowercase variant some call sites use.
func isVMNotFoundMessage(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "VM not found") || strings.Contains(msg, "vm not found")
}

// mapVMNotFound rewrites untyped VM-not-found errors to
// codes.NotFound. Errors that already carry a non-Unknown gRPC code
// pass through untouched.
func mapVMNotFound(err error) error {
	if err == nil {
		return nil
	}
	if status.Code(err) != codes.Unknown {
		return err
	}
	if !isVMNotFoundMessage(err) {
		return err
	}
	return status.Error(codes.NotFound, err.Error())
}

func vmNotFoundUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		return resp, mapVMNotFound(err)
	}
}

func vmNotFoundStreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		return mapVMNotFound(handler(srv, ss))
	}
}
