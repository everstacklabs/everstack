package v1

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/jmoiron/sqlx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	sandboxlc "github.com/everstacklabs/everstack/internal/orchestrator/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1/agentsconnect"
)

func ownershipTestManager() *sandbox.SandboxManager {
	m := &sandbox.SandboxManager{}
	m.SeedInstancesForTest(map[string]*sandbox.Instance{
		"sbx-a": {ID: "sbx-a", Name: "web", LifecycleState: sandbox.LifecycleRunning, Config: sandbox.InstanceConfig{TenantID: "tenant-a"}},
		"sbx-b": {ID: "sbx-b", Name: "web", LifecycleState: sandbox.LifecycleRunning, Config: sandbox.InstanceConfig{TenantID: "tenant-b"}},
	})
	return m
}

func collisionLifecycleTestServer(t *testing.T) (*Server, sqlmock.Sqlmock) {
	t.Helper()
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	db := sqlx.NewDb(rawDB, "sqlmock")
	return &Server{
		sandboxMgr:              collisionTestManager(),
		sandboxOwnershipEnforce: true,
		lifecycleRepo:           sandboxlc.NewRepository(db),
	}, mock
}

func expectCollisionStopTargetsOwner(mock sqlmock.Sqlmock) {
	mock.ExpectExec(regexp.QuoteMeta("UPDATE sandbox_instances")).
		WithArgs("sbx-a", sandboxlc.DesireSleeping).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestConnectHandlerUsesCanonicalOwnedSandboxID(t *testing.T) {
	s, mock := collisionLifecycleTestServer(t)
	expectCollisionStopTargetsOwner(mock)
	auth := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			return next(contextkeys.WithTenantID(ctx, "tenant-a"), req)
		}
	})
	path, handler := s.RegisterConnectServer(connect.WithInterceptors(auth))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := agentsconnect.NewAgentsServiceClient(server.Client(), server.URL)
	if _, err := client.StopSandbox(context.Background(), connect.NewRequest(&agentspb.StopSandboxRequest{SandboxId: "sbx-b"})); err != nil {
		t.Fatalf("StopSandbox through Connect: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRESTWrapperUsesCanonicalOwnedSandboxID(t *testing.T) {
	s, mock := collisionLifecycleTestServer(t)
	expectCollisionStopTargetsOwner(mock)
	gatewayMux := runtime.NewServeMux()
	if err := s.RegisterGateway(context.Background(), gatewayMux, "", nil); err != nil {
		t.Fatalf("register gateway: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/sandbox/sbx-b/stop", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(contextkeys.WithTenantID(req.Context(), "tenant-a"))
	rec := httptest.NewRecorder()
	gatewayMux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("StopSandbox through REST = %d, body %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetSandboxInstanceResolvesOnlyInsideCallerScope(t *testing.T) {
	s := &Server{sandboxMgr: collisionTestManager()}

	resp, err := s.GetSandboxInstance(contextkeys.WithTenantID(context.Background(), "tenant-a"), connect.NewRequest(&agentspb.GetSandboxInstanceRequest{SandboxId: "sbx-b"}))
	if err != nil {
		t.Fatalf("GetSandboxInstance by colliding name: %v", err)
	}
	if got := resp.Msg.GetInstance().GetId(); got != "sbx-a" {
		t.Fatalf("resolved sandbox = %q, want sbx-a", got)
	}

	_, err = s.GetSandboxInstance(contextkeys.WithTenantID(context.Background(), "tenant-a"), connect.NewRequest(&agentspb.GetSandboxInstanceRequest{SandboxId: "victim"}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("foreign sandbox error = %v, want NotFound", err)
	}
}

// TestSandboxOwnershipInterceptor exercises the ConnectRPC ownership
// interceptor: in enforce mode a caller cannot reach another tenant's sandbox
// (by id or colliding name) and 404s; the owner passes; in audit mode a
// cross-tenant call is allowed through (logged only).
func TestSandboxOwnershipInterceptor(t *testing.T) {
	t.Parallel()

	okNext := func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&agentspb.StopSandboxResponse{}), nil
	}

	call := func(s *Server, tenant, sandboxID string) (connect.AnyResponse, error) {
		ic := s.sandboxOwnershipInterceptor()
		wrapped := ic.WrapUnary(okNext)
		ctx := contextkeys.WithTenantID(context.Background(), tenant)
		req := connect.NewRequest(&agentspb.StopSandboxRequest{SandboxId: sandboxID})
		return wrapped(ctx, req)
	}

	t.Run("enforce: owner passes", func(t *testing.T) {
		s := &Server{sandboxMgr: ownershipTestManager(), sandboxOwnershipEnforce: true}
		if _, err := call(s, "tenant-b", "sbx-b"); err != nil {
			t.Fatalf("owner should pass, got %v", err)
		}
	})

	t.Run("enforce: cross-tenant exact id is 404", func(t *testing.T) {
		s := &Server{sandboxMgr: ownershipTestManager(), sandboxOwnershipEnforce: true}
		_, err := call(s, "tenant-a", "sbx-b")
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("cross-tenant exact id should be NotFound, got %v", err)
		}
	})

	t.Run("enforce: colliding name resolves only within tenant", func(t *testing.T) {
		s := &Server{sandboxMgr: ownershipTestManager(), sandboxOwnershipEnforce: true}
		// "web" exists in both tenants; tenant-a must get its own, not be denied.
		if _, err := call(s, "tenant-a", "web"); err != nil {
			t.Fatalf("tenant-a should resolve its own 'web', got %v", err)
		}
	})

	t.Run("audit: cross-tenant allowed through (logged only)", func(t *testing.T) {
		s := &Server{sandboxMgr: ownershipTestManager(), sandboxOwnershipEnforce: false}
		if _, err := call(s, "tenant-a", "sbx-b"); err != nil {
			t.Fatalf("audit mode must not reject, got %v", err)
		}
	})

	t.Run("default constructor enforces", func(t *testing.T) {
		s := CreateServer()
		s.SetSandboxManager(ownershipTestManager())
		_, err := call(s, "tenant-a", "sbx-b")
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("default server should enforce ownership, got %v", err)
		}
	})
}

func TestGrpcServerSandboxOwnership(t *testing.T) {
	grpcServerType := reflect.TypeOf((*GrpcServer)(nil))
	contextType := reflect.TypeOf((*context.Context)(nil)).Elem()
	errorType := reflect.TypeOf((*error)(nil)).Elem()

	var methods []reflect.Method
	for i := 0; i < grpcServerType.NumMethod(); i++ {
		method := grpcServerType.Method(i)
		if method.Type.NumIn() != 3 || method.Type.NumOut() != 2 {
			continue
		}
		if method.Type.In(1) != contextType || !method.Type.Out(1).Implements(errorType) {
			continue
		}
		requestType := method.Type.In(2)
		if requestType.Kind() != reflect.Pointer || requestType.Elem().Kind() != reflect.Struct {
			continue
		}
		field, ok := requestType.Elem().FieldByName("SandboxId")
		if !ok || field.Type.Kind() != reflect.String {
			continue
		}
		methods = append(methods, method)
	}

	if len(methods) == 0 {
		t.Fatal("no GrpcServer sandbox-id methods found")
	}
	t.Logf("covered %d GrpcServer sandbox-id methods", len(methods))

	for _, method := range methods {
		t.Run(method.Name, func(t *testing.T) {
			grpcServer := &GrpcServer{base: &Server{
				sandboxMgr:              ownershipTestManager(),
				sandboxOwnershipEnforce: true,
			}}
			request := reflect.New(method.Type.In(2).Elem())
			request.Elem().FieldByName("SandboxId").SetString("sbx-a")
			ctx := contextkeys.WithTenantID(context.Background(), "tenant-b")

			var results []reflect.Value
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						t.Fatalf("base handler was reached and panicked: %v", recovered)
					}
				}()
				results = method.Func.Call([]reflect.Value{
					reflect.ValueOf(grpcServer),
					reflect.ValueOf(ctx),
					request,
				})
			}()

			if len(results) != 2 {
				t.Fatalf("returned %d values, want 2", len(results))
			}
			var err error
			if !results[1].IsNil() {
				err = results[1].Interface().(error)
			}
			if got := status.Code(err); got != codes.NotFound {
				t.Fatalf("status code = %v, want %v (error: %v)", got, codes.NotFound, err)
			}
		})
	}
}

func TestRegisterGatewayEnforcesSandboxOwnership(t *testing.T) {
	manager := &sandbox.SandboxManager{}
	manager.SeedLiveInstancesForTest(map[string]*sandbox.Instance{
		"sbx-a": {ID: "sbx-a", Name: "web", LifecycleState: sandbox.LifecycleRunning, Config: sandbox.InstanceConfig{TenantID: "tenant-a", SessionID: "session-a"}},
		"sbx-b": {ID: "sbx-b", Name: "web", LifecycleState: sandbox.LifecycleRunning, Config: sandbox.InstanceConfig{TenantID: "tenant-b", SessionID: "session-b"}},
	})
	base := &Server{
		sandboxMgr:              manager,
		sandboxOwnershipEnforce: true,
	}
	gatewayMux := runtime.NewServeMux()
	if err := base.RegisterGateway(context.Background(), gatewayMux, "", nil); err != nil {
		t.Fatalf("register gateway: %v", err)
	}

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.Header.Get("X-Test-Tenant-ID")
		ctx := contextkeys.WithTenantID(r.Context(), tenantID)
		gatewayMux.ServeHTTP(w, r.WithContext(ctx))
	}))
	t.Cleanup(testServer.Close)

	getSandbox := func(tenantID string) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, testServer.URL+"/v1/sandbox/instances/sbx-a", nil)
		if err != nil {
			t.Fatalf("create request: %v", err)
		}
		req.Header.Set("X-Test-Tenant-ID", tenantID)
		resp, err := testServer.Client().Do(req)
		if err != nil {
			t.Fatalf("GET sandbox as %s: %v", tenantID, err)
		}
		defer resp.Body.Close()
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			t.Fatalf("read response as %s: %v", tenantID, err)
		}
		return resp.StatusCode
	}

	if got := getSandbox("tenant-b"); got != http.StatusNotFound {
		t.Fatalf("foreign tenant status = %d, want %d", got, http.StatusNotFound)
	}
	if got := getSandbox("tenant-a"); got != http.StatusOK {
		t.Fatalf("owner status = %d, want %d", got, http.StatusOK)
	}
}

// The gateway passes its authentication interceptors to RegisterConnectServer.
// The ownership check must run after them, or it never sees the caller's
// tenant: a caller authenticated by such an interceptor must reach their own
// sandbox and get NotFound for another tenant's.
func TestSandboxOwnershipRunsAfterTheCallersAuthInterceptors(t *testing.T) {
	s := &Server{sandboxMgr: ownershipTestManager(), sandboxOwnershipEnforce: true}
	auth := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			return next(contextkeys.WithTenantID(ctx, req.Header().Get("X-Test-Tenant")), req)
		}
	})
	path, handler := s.RegisterConnectServer(connect.WithInterceptors(auth))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := agentsconnect.NewAgentsServiceClient(server.Client(), server.URL)

	stop := func(tenant, sandboxID string) error {
		req := connect.NewRequest(&agentspb.StopSandboxRequest{SandboxId: sandboxID})
		req.Header().Set("X-Test-Tenant", tenant)
		_, err := client.StopSandbox(context.Background(), req)
		return err
	}
	if err := stop("tenant-b", "sbx-a"); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("another tenant's sandbox = %v, want NotFound from the ownership check", err)
	}
	err := stop("tenant-b", "sbx-b")
	if code := connect.CodeOf(err); code == connect.CodePermissionDenied || (code == connect.CodeNotFound && strings.Contains(err.Error(), "sandbox not found")) {
		t.Fatalf("the owner was stopped by the ownership check: %v", err)
	}
}
