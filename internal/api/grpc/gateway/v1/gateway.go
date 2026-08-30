package v1

import (
	"context"

	"connectrpc.com/connect"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
)

// RPCs
func (s *Server) GetGateway(_ context.Context, _ *connect.Request[gatewaypb.GetGatewayRequest]) (*connect.Response[gatewaypb.GetGatewayResponse], error) {
	return connect.NewResponse(&gatewaypb.GetGatewayResponse{}), nil
}

// Classic gRPC GetGateway
func (g *GrpcServer) GetGateway(ctx context.Context, _ *gatewaypb.GetGatewayRequest) (*gatewaypb.GetGatewayResponse, error) {
	_ = ctx
	return &gatewaypb.GetGatewayResponse{}, nil
}
