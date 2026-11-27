package server

import (
	"context"
	"crypto/tls"
	"fmt"
	apikeylib "github.com/everstacklabs/everstack/internal/lib/apikey"
	"net/http"
	"strings"

	"github.com/everstacklabs/everstack/internal/activity"
	"github.com/everstacklabs/everstack/internal/api/common"
	client_middleware "github.com/everstacklabs/everstack/internal/api/grpc/client/middleware"
	http_mw "github.com/everstacklabs/everstack/internal/api/http/middleware/interceptors"
	"github.com/everstacklabs/everstack/internal/api/internalauth"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
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
	gwDirect   grpc.ClientConnInterface
)

// GatewayMux returns the mux used by the grpc-gateway.
func GatewayMux() *runtime.ServeMux { return gwMux }

// GatewayEndpoint returns the backend gRPC endpoint the gateway dials.
func GatewayEndpoint() string { return gwEndpoint }

// GatewayDialOpts returns the dial options used by the gateway.
func GatewayDialOpts() []grpc.DialOption { return gwDialOpts }

// GatewayDirectConn returns the connection that runs this process's handlers
// without HTTP/2, or nil when the operator chose a network or in-memory
// HTTP/2 transport. Services implementing GatewayClientRegistrable register
// on it.
func GatewayDirectConn() grpc.ClientConnInterface { return gwDirect }

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
		runtime.WithMetadata(gatewayRequestMetadata),
	)

	endpoint := fmt.Sprintf("%s:%d", host, port)
	tracing := client_middleware.DefaultTracingClient()
	dialOpts := []grpc.DialOption{
		grpc.WithChainUnaryInterceptor(
			client_middleware.UnaryActivityClientInterceptor(),
		),
		grpc.WithStatsHandler(tracing),
	}
	var direct grpc.ClientConnInterface
	if loopback := LoopbackFromContext(ctx); loopback != nil {
		if loopback.direct {
			// The same client-side tracing and activity metadata as the
			// dialled connection below; see direct.go.
			direct = loopback.ClientConn(endpoint,
				WithDirectStatsHandler(tracing),
				WithDirectUnaryInterceptor(client_middleware.UnaryActivityClientInterceptor()),
			)
		}
		// The caller serves the handlers in this process: call them over the
		// in-memory transport instead of dialing our own address. See Loopback.
		endpoint = loopback.Target(endpoint)
		dialOpts = append(dialOpts, loopback.DialOptions()...)
	} else {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(selectTransportCreds(tlsConfig)))
	}

	if direct != nil {
		err := gatewayv1.RegisterGatewayServiceHandlerClient(ctx, mux, gatewayv1.NewGatewayServiceClient(direct))
		if err != nil {
			return nil, fmt.Errorf("register gateway service: %w", err)
		}
	} else if err := gatewayv1.RegisterGatewayServiceHandlerFromEndpoint(ctx, mux, endpoint, dialOpts); err != nil {
		return nil, fmt.Errorf("register gateway service: %w", err)
	}

	// Expose for per-service registration
	gwMux = mux
	gwEndpoint = endpoint
	gwDialOpts = dialOpts
	gwDirect = direct

	// Wrap with lightweight HTTP middlewares already used elsewhere
	var handler http.Handler = mux
	handler = http_mw.CallDurationHandler(handler)
	handler = http_mw.CORSInterceptor(handler)

	return StripClientGatewayIdentity(handler), nil
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

// gatewayRequestMetadata forwards credential identity only from verified context.
func gatewayRequestMetadata(ctx context.Context, req *http.Request) metadata.MD {
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
	// Never forward an incoming identity header. Mint only from verified context.
	if hash := contextkeys.GetAPIKeyHash(req.Context()); hash != "" && contextkeys.IsTenantAuthenticated(req.Context()) {
		identity := internalauth.GatewayIdentity{TenantID: contextkeys.GetTenantID(req.Context()), KeyID: contextkeys.GetAPIKeyID(req.Context()), Hash: hash, Surfaces: contextkeys.GetAPIKeySurfaces(req.Context()), RateLimitRPM: contextkeys.GetAPIKeyRateLimit(req.Context()), Processed: apikeylib.Processed(req.Context(), contextkeys.GetAPIKeyID(req.Context()), apikeylib.SurfaceInference)}
		pairs = append(pairs, internalauth.GatewayIdentityHeader, internalauth.SignGatewayIdentity(identity))
		transport := http.Header{}
		internalauth.SetGatewayTransportHeader(transport)
		pairs = append(pairs, internalauth.GatewayTransportHeader, transport.Get(internalauth.GatewayTransportHeader))
	}
	return metadata.Pairs(pairs...)
}

// StripClientGatewayIdentity runs at the HTTP edge, before authentication and
// grpc-gateway's automatic metadata forwarding. Only the process-owned loopback
// transport may carry a normal signed envelope. Prefixed copies are never used.
func StripClientGatewayIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		owned := internalauth.IsGatewayTransport(r.Header)
		r = r.Clone(r.Context())
		for name := range r.Header {
			if strings.EqualFold(name, "Grpc-Metadata-"+internalauth.GatewayIdentityHeader) || strings.EqualFold(name, "Grpc-Metadata-"+internalauth.GatewayTransportHeader) || !owned && (strings.EqualFold(name, internalauth.GatewayIdentityHeader) || strings.EqualFold(name, internalauth.GatewayTransportHeader)) {
				delete(r.Header, name)
			}
		}
		next.ServeHTTP(w, r)
	})
}
