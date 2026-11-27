package main

import (
	"context"
	"strings"
	"sync/atomic"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// agentServicePrefix matches every method on FirecrackerAgent.
// Health and reflection use different prefixes and pass through the
// gate so kubelet probes and grpcurl debugging keep working while
// preflight is still red.
const agentServicePrefix = "/everstack.firecracker.v1.FirecrackerAgent/"

// readiness flips to true once preflight has passed and the backend
// is wired into the agent server. Demoting back to false during a
// regression makes the unary/stream interceptors reject new work
// instead of letting it land on a host that lost its rootfs/kernel.
type readiness struct {
	ok     atomic.Bool
	reason atomic.Pointer[string]
}

func (r *readiness) Set(v bool)    { r.ok.Store(v) }
func (r *readiness) IsReady() bool { return r.ok.Load() }

// SetReason records which gate is holding the agent back so a rejected call
// identifies the failed check instead of returning a generic preflight reason.
func (r *readiness) SetReason(reason string) { r.reason.Store(&reason) }

// notReadyError is what a call gets while the agent is not ready.
func (r *readiness) notReadyError() error {
	reason := "preflight not yet passed"
	if p := r.reason.Load(); p != nil && *p != "" {
		reason = *p
	}
	return status.Error(codes.Unavailable, "firecracker-agent: not serving: "+reason)
}

func notReadyUnaryInterceptor(r *readiness) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !r.IsReady() && strings.HasPrefix(info.FullMethod, agentServicePrefix) {
			return nil, r.notReadyError()
		}
		return handler(ctx, req)
	}
}

func notReadyStreamInterceptor(r *readiness) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !r.IsReady() && strings.HasPrefix(info.FullMethod, agentServicePrefix) {
			return r.notReadyError()
		}
		return handler(srv, ss)
	}
}
