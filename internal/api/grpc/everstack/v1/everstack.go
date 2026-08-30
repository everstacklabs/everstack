package v1

import (
	"context"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/health/v1"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *Server) Health(ctx context.Context, req *connect.Request[emptypb.Empty]) (*connect.Response[health.HealthResponse], error) {
	return &connect.Response[health.HealthResponse]{
		Msg: &health.HealthResponse{
			Status: "healthy",
		},
	}, nil
}

func (s *Server) GetSystemInfo(ctx context.Context, req *connect.Request[emptypb.Empty]) (*connect.Response[health.SystemInfoResponse], error) {
	return &connect.Response[health.SystemInfoResponse]{
		Msg: &health.SystemInfoResponse{
			Version: "1.0.0",
		},
	}, nil
}
