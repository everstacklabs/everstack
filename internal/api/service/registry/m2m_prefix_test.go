package registry

import (
	"context"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/everstacklabs/everstack/internal/api/grpc/server/middleware"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1/gatewayconnect"
)

type fakeGatewayService struct{}

type procedureRequest struct {
	connect.AnyRequest
	procedure string
}

func (r procedureRequest) Spec() connect.Spec {
	return connect.Spec{StreamType: connect.StreamTypeUnary, Procedure: r.procedure}
}

func (fakeGatewayService) AppName() string      { return gatewayconnect.GatewayServiceName }
func (fakeGatewayService) MethodPrefix() string { return gatewayconnect.GatewayServiceName }
func (fakeGatewayService) RegisterConnectServer(opts ...connect.HandlerOption) (string, http.Handler) {
	return "/" + gatewayconnect.GatewayServiceName + "/", http.NotFoundHandler()
}
func (fakeGatewayService) FileDescriptor() protoreflect.FileDescriptor {
	return gatewaypb.File_everstack_gateway_v1_gateway_proto
}

// Billing calls GetUpgradeCallbackSecret on the gateway with M2M headers
// and no API key. Since #772 the API-key interceptor ignores M2M headers
// entirely and the procedure is policy-bypassed instead; this drives the
// exact call billing makes (real HMAC headers, no API key) through the
// registry-built chain so that path cannot regress again (it did once,
// via a doubled service prefix: 502 during upgrade-session creation).
func TestBuildInterceptors_M2MSignedGatewayCallPassesAPIKeyGate(t *testing.T) {
	const procedure = "/everstack.gateway.v1.GatewayService/GetUpgradeCallbackSecret"
	reg := New()
	chain := reg.BuildInterceptors(fakeGatewayService{})

	next := connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&gatewaypb.GetUpgradeCallbackSecretResponse{Found: true}), nil
	})
	for i := len(chain) - 1; i >= 0; i-- {
		next = chain[i].WrapUnary(next)
	}

	// Connect fills Spec on the wire; present the procedure the way the
	// server-side interceptors see it.
	req := procedureRequest{AnyRequest: connect.NewRequest(&gatewaypb.GetUpgradeCallbackSecretRequest{CallbackSecret: "cb"}), procedure: procedure}
	key := []byte("m2m-signing-key")
	ts := time.Now().UTC().Format(time.RFC3339)
	nonce := "nonce-1"
	req.Header().Set(middleware.HeaderServiceToken, "billing")
	req.Header().Set(middleware.HeaderTimestamp, ts)
	req.Header().Set(middleware.HeaderNonce, nonce)
	req.Header().Set(middleware.HeaderSignature, middleware.ComputeSignature(http.MethodPost, procedure, nil, ts, nonce, key))

	if _, err := next(context.Background(), req); err != nil {
		t.Fatalf("signed M2M call to %s rejected by the registry chain: %v", procedure, err)
	}
}

// No header-presence gate may return (EVS-324 item 15): forged M2M headers
// on a GatewayService procedure that is not self-authenticating must still
// be rejected for lack of an API key.
func TestBuildInterceptors_ForgedM2MHeadersDoNotAuthenticate(t *testing.T) {
	const procedure = "/everstack.gateway.v1.GatewayService/GetGateway"
	chain := New().BuildInterceptors(fakeGatewayService{})
	reached := false
	next := connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		reached = true
		return connect.NewResponse(&gatewaypb.GetUpgradeCallbackSecretResponse{}), nil
	})
	for i := len(chain) - 1; i >= 0; i-- {
		next = chain[i].WrapUnary(next)
	}
	req := procedureRequest{AnyRequest: connect.NewRequest(&gatewaypb.GetUpgradeCallbackSecretRequest{}), procedure: procedure}
	req.Header().Set(middleware.HeaderServiceToken, "forged")
	req.Header().Set(middleware.HeaderSignature, "forged")
	req.Header().Set("Authorization", "Bearer forged")
	if _, err := next(context.Background(), req); connect.CodeOf(err) != connect.CodeUnauthenticated || reached {
		t.Fatalf("forged M2M headers: want Unauthenticated and handler not reached, got %v (reached=%v)", err, reached)
	}
}
