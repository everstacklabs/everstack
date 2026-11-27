package middleware_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	// Import the gateway's composition root so the global registry includes
	// every generated service used by startup, including optional services.
	_ "github.com/everstacklabs/everstack/cmd/serve"
	"github.com/everstacklabs/everstack/internal/api/common"
	mw "github.com/everstacklabs/everstack/internal/api/grpc/server/middleware"
	"github.com/everstacklabs/everstack/internal/api/internalauth"
	"github.com/everstacklabs/everstack/internal/cqrs"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/query"
	apikeyq "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/emptypb"
)

type surfaceStore struct {
	surfaces []string
	calls    int
}

func (s *surfaceStore) QueryType() string { return "GetApiKeyByHash" }
func (s *surfaceStore) Handle(_ context.Context, q query.Query) (interface{}, error) {
	if !q.(*apikeyq.GetApiKeyByHashQuery).CallerEnforces {
		panic("interceptor must declare per-request enforcement")
	}
	s.calls++
	tenant := "tenant"
	return apikeyq.APIKeyReadModel{ID: "bot-key", OrgID: &tenant, Surfaces: s.surfaces}, nil
}

// This test intentionally enumerates a superset of mounted procedures from
// generated descriptors, not a handwritten service/method list. New services
// imported by gateway startup automatically enter this security assertion.
func TestBotSurfacesEveryGatewayProcedure(t *testing.T) {
	procedures := map[string]protoreflect.MethodDescriptor{}
	protoregistry.GlobalFiles.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		services := file.Services()
		for i := 0; i < services.Len(); i++ {
			service := services.Get(i)
			if !strings.HasPrefix(string(service.FullName()), "everstack.") {
				continue
			}
			for j := 0; j < service.Methods().Len(); j++ {
				method := service.Methods().Get(j)
				procedures["/"+string(service.FullName())+"/"+string(method.Name())] = method
			}
		}
		return true
	})
	if len(procedures) < 100 {
		t.Fatalf("startup descriptors not linked: %d procedures", len(procedures))
	}
	t.Logf("Enumerated %d generated Everstack procedures", len(procedures))
	for _, set := range []struct {
		name     string
		surfaces []string
	}{
		{"all", []string{"inference", "mcp", "telemetry", "a2a"}},
		{"mcp", []string{"mcp"}}, {"empty", []string{}},
	} {
		t.Run(set.name, func(t *testing.T) {
			store := &surfaceStore{surfaces: set.surfaces}
			bus := query.NewQueryBus()
			bus.RegisterHandler(store)
			ctx := cqrs.WithSystem(context.Background(), &cqrs.System{QueryBus: bus})
			ctx = contextkeys.WithAPIKeyHashSecret(ctx, "test-secret")
			interceptor := mw.NewAPIKeyInterceptor(false)
			for procedure, descriptor := range procedures {
				// Keep the expected allowlist literal and independent of production code.
				allowed := set.name == "all" && (procedure == "/everstack.gateway.v1.GatewayService/ChatCompletion" || procedure == "/everstack.gateway.v1.GatewayService/CreateResponse" || procedure == "/everstack.gateway.v1.GatewayService/Embeddings" || procedure == "/everstack.gateway.v1.GatewayService/ListModels")
				for _, mode := range []string{"cold", "warm", "context", "forwarded"} {
					if mode == "cold" {
						interceptor.ClearCache()
					}
					header := http.Header{}
					header.Set(common.EverstackApiKey, "sk-test")
					requestCtx := ctx
					if mode == "context" {
						requestCtx = contextkeys.WithAuthenticatedAPIKeyID(ctx, "tenant", "verified-digest", "bot-key")
						requestCtx = contextkeys.WithAPIKeySurfaces(requestCtx, set.surfaces)
					}
					if mode == "forwarded" {
						header.Del(common.EverstackApiKey)
						header.Set(internalauth.GatewayIdentityHeader, internalauth.SignGatewayIdentity(internalauth.GatewayIdentity{TenantID: "tenant", Hash: "verified-digest", KeyID: "bot-key", Surfaces: set.surfaces}))
					}
					reached := false
					handler := connect.NewUnaryHandler(procedure, func(context.Context, *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
						reached = true
						return connect.NewResponse(&emptypb.Empty{}), nil
					}, connect.WithInterceptors(interceptor))
					request := httptest.NewRequest("POST", procedure, strings.NewReader("{}"))
					request.Header = header
					request.Header.Set("Content-Type", "application/json")
					rec := httptest.NewRecorder()
					handler.ServeHTTP(rec, request.WithContext(requestCtx))
					if reached != allowed {
						t.Fatalf("%s %s: reached=%v allowed=%v response=%s", procedure, mode, reached, allowed, rec.Body.String())
					}
					if !allowed {
						var body struct{ Code, Message string }
						_ = json.Unmarshal(rec.Body.Bytes(), &body)
						if rec.Code != 403 || body.Code != "permission_denied" || body.Message != "this API key cannot call this API" {
							t.Fatalf("%s %s: %d %s", procedure, mode, rec.Code, rec.Body.String())
						}
					}
				}
				// Exercise the actual streaming interceptor for every streaming method.
				if descriptor.IsStreamingServer() {
					reached := false
					handler := connect.NewServerStreamHandler(procedure, func(context.Context, *connect.Request[emptypb.Empty], *connect.ServerStream[emptypb.Empty]) error {
						reached = true
						return nil
					}, connect.WithInterceptors(interceptor))
					request := httptest.NewRequest("POST", procedure, strings.NewReader("\x00\x00\x00\x00\x02{}"))
					request.Header.Set("Content-Type", "application/connect+json")
					request.Header.Set(common.EverstackApiKey, "sk-test")
					rec := httptest.NewRecorder()
					handler.ServeHTTP(rec, request.WithContext(ctx))
					if reached != allowed || (!allowed && !strings.Contains(rec.Body.String(), "permission_denied")) {
						t.Fatalf("streaming %s: reached=%v %s", procedure, reached, rec.Body.String())
					}
				}
			}
		})
	}
}
