package v1

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	hostingpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/hosting/v1"
)

func TestHostingCapabilitiesNeverGrantCustomerGatewayOperatorAccess(t *testing.T) {
	t.Parallel()

	server := CreateServerWithDeps(context.Background(), nil, nil, Config{
		BaseDomain:     "sites.example",
		AllowAnonymous: true,
	})
	server.SetEdgeEnforcementConfigured(true)
	response, err := server.GetHostingCapabilities(
		context.Background(),
		connect.NewRequest(&hostingpb.HostingCapabilitiesRequest{}),
	)
	if err != nil {
		t.Fatalf("GetHostingCapabilities() error = %v", err)
	}
	if response.Msg.GetCanOperate() {
		t.Fatal("customer gateway unexpectedly grants operator access")
	}
	if response.Msg.GetBaseDomain() != "sites.example" {
		t.Fatalf("base domain = %q", response.Msg.GetBaseDomain())
	}
	if !response.Msg.GetAnonymousPublishingEnabled() ||
		!response.Msg.GetEdgeEnforcementConfigured() {
		t.Fatalf("capabilities = %#v", response.Msg)
	}
}
