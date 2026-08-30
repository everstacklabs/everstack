package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"

	"github.com/everstacklabs/everstack/internal/activity"
	"github.com/everstacklabs/everstack/internal/api/common"
	client_middleware "github.com/everstacklabs/everstack/internal/api/grpc/client/middleware"
	http_mw "github.com/everstacklabs/everstack/internal/api/http/middleware/interceptors"
	gatewayv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
)

var (
	gwMux      *runtime.ServeMux
	gwEndpoint string
	gwDialOpts []grpc.DialOption
)

// GatewayMux returns the mux used by the grpc-gateway.
func GatewayMux() *runtime.ServeMux { return gwMux }

// GatewayEndpoint returns the backend gRPC endpoint the gateway dials.
func GatewayEndpoint() string { return gwEndpoint }

// GatewayDialOpts returns the dial options used by the gateway.
func GatewayDialOpts() []grpc.DialOption { return gwDialOpts }

// NewGRPCJSONGateway creates a JSON REST gateway that forwards to the in-process
// ConnectRPC server (which also serves gRPC) listening on the provided port.
// It returns an http.Handler that can be mounted alongside your Connect handlers.
func NewGRPCJSONGateway(ctx context.Context, host string, port uint16, tlsConfig *tls.Config) (http.Handler, error) {
	mux := runtime.NewServeMux(
		// Defaults are fast and fine; we can customize JSONPb if needed later
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
			MarshalOptions: protojson.MarshalOptions{
				UseProtoNames:   true,
				EmitUnpopulated: false, // omit empty fields
				Multiline:       true,
				UseEnumNumbers:  true,
			},
			UnmarshalOptions: protojson.UnmarshalOptions{
				DiscardUnknown: true,
			},
		}),
		// Forward select HTTP request data and sticky-key headers to backend via gRPC metadata
		runtime.WithMetadata(func(ctx context.Context, req *http.Request) metadata.MD {
			pairs := []string{
				activity.PathKey, req.URL.Path,
				activity.RequestMethodKey, req.Method,
			}
			appendHeader := func(key string) {
				if v := req.Header.Get(key); v != "" {
					pairs = append(pairs, strings.ToLower(key), v)
				}
			}
			// Headers used for sticky-key selection. Forward the canonical
			// x-evs-api-key AND the legacy x-mf-*/x-everstack-* names so handlers
			// that read the api key from gRPC metadata keep working for deployed
			// clients (add-both, never replace).
			appendHeader(common.Authorization)
			appendHeader(common.EverstackApiKey)
			appendHeader(common.LegacyMFApiKey)
			appendHeader(common.LegacyEverstackApiKey)
			appendHeader(common.XUserID)
			appendHeader(common.ForwardedFor)
			appendHeader("x-real-ip")
			// Forward security headers for same-origin detection in gRPC interceptor
			appendHeader("sec-fetch-site")
			appendHeader(common.Origin)
			appendHeader("referer")
			// Forward mock header explicitly so grpc handlers can short-circuit
			// (canonical + legacy).
			appendHeader("x-evs-mock")
			appendHeader("x-mf-mock")
			return metadata.Pairs(pairs...)
		}),
	)

	endpoint := fmt.Sprintf("%s:%d", host, port)
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(selectTransportCreds(tlsConfig)),
		grpc.WithChainUnaryInterceptor(
			client_middleware.UnaryActivityClientInterceptor(),
		),
		grpc.WithStatsHandler(client_middleware.DefaultTracingClient()),
	}

	if err := gatewayv1.RegisterGatewayServiceHandlerFromEndpoint(ctx, mux, endpoint, dialOpts); err != nil {
		return nil, fmt.Errorf("register gateway service: %w", err)
	}

	// Expose for per-service registration
	gwMux = mux
	gwEndpoint = endpoint
	gwDialOpts = dialOpts

	// Wrap with lightweight HTTP middlewares already used elsewhere
	var handler http.Handler = mux
	handler = http_mw.CallDurationHandler(handler)
	handler = http_mw.CORSInterceptor(handler)

	return handler, nil
}

func selectTransportCreds(tlsConfig *tls.Config) credentials.TransportCredentials {
	if tlsConfig == nil {
		return insecure.NewCredentials()
	}
	clone := tlsConfig.Clone()
	// When dialing localhost we can skip verify; otherwise preserve proper verification
	// Note: caller can set ServerName if needed when host is not localhost via tlsConfig
	clone.InsecureSkipVerify = false
	return credentials.NewTLS(clone)
}
