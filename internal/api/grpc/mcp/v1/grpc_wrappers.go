package v1

import (
	"context"

	"connectrpc.com/connect"
	mcppb "github.com/everstacklabs/everstack/pkg/grpc/everstack/mcp/v1"
)

// Classic gRPC wrapper methods that delegate to the Connect-based Server.

func (g *GrpcServer) RegisterMcpServer(ctx context.Context, req *mcppb.RegisterMcpServerRequest) (*mcppb.RegisterMcpServerResponse, error) {
	cReq := &connect.Request[mcppb.RegisterMcpServerRequest]{Msg: req}
	resp, err := g.base.RegisterMcpServer(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetMcpServer(ctx context.Context, req *mcppb.GetMcpServerRequest) (*mcppb.GetMcpServerResponse, error) {
	cReq := &connect.Request[mcppb.GetMcpServerRequest]{Msg: req}
	resp, err := g.base.GetMcpServer(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListMcpServers(ctx context.Context, req *mcppb.ListMcpServersRequest) (*mcppb.ListMcpServersResponse, error) {
	cReq := &connect.Request[mcppb.ListMcpServersRequest]{Msg: req}
	resp, err := g.base.ListMcpServers(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) UpdateMcpServer(ctx context.Context, req *mcppb.UpdateMcpServerRequest) (*mcppb.UpdateMcpServerResponse, error) {
	cReq := &connect.Request[mcppb.UpdateMcpServerRequest]{Msg: req}
	resp, err := g.base.UpdateMcpServer(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) DeleteMcpServer(ctx context.Context, req *mcppb.DeleteMcpServerRequest) (*mcppb.DeleteMcpServerResponse, error) {
	cReq := &connect.Request[mcppb.DeleteMcpServerRequest]{Msg: req}
	resp, err := g.base.DeleteMcpServer(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) DiscoverTools(ctx context.Context, req *mcppb.DiscoverToolsRequest) (*mcppb.DiscoverToolsResponse, error) {
	cReq := &connect.Request[mcppb.DiscoverToolsRequest]{Msg: req}
	resp, err := g.base.DiscoverTools(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) ListFederatedTools(ctx context.Context, req *mcppb.ListFederatedToolsRequest) (*mcppb.ListFederatedToolsResponse, error) {
	cReq := &connect.Request[mcppb.ListFederatedToolsRequest]{Msg: req}
	resp, err := g.base.ListFederatedTools(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) CallTool(ctx context.Context, req *mcppb.CallToolRequest) (*mcppb.CallToolResponse, error) {
	cReq := &connect.Request[mcppb.CallToolRequest]{Msg: req}
	resp, err := g.base.CallTool(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (g *GrpcServer) GetMcpServerHealth(ctx context.Context, req *mcppb.GetMcpServerHealthRequest) (*mcppb.GetMcpServerHealthResponse, error) {
	cReq := &connect.Request[mcppb.GetMcpServerHealthRequest]{Msg: req}
	resp, err := g.base.GetMcpServerHealth(ctx, cReq)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}
