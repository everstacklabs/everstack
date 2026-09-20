package v1

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/agents/openaiapi"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	pb "github.com/everstacklabs/everstack/pkg/grpc/everstack/openai_agents/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/openai_agents/v1/openaiagentsconnect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"
)

type stubStore struct {
	openaiapi.Store
	session *openaiapi.Session
	events  []openaiapi.Event
}

func headerAuthInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
			if r.Header().Get("Authorization") != "Bearer fixture-token" {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing authorization"))
			}
			return next(authenticated(ctx), r)
		}
	}
}

func (s *stubStore) ListEvents(context.Context, openaiapi.Ownership, string, int64, int) ([]openaiapi.Event, error) {
	return append([]openaiapi.Event(nil), s.events...), nil
}

func (s *stubStore) ListConnections(context.Context, openaiapi.Ownership) ([]openaiapi.Connection, error) {
	return nil, nil
}

func (s *stubStore) GetSession(context.Context, openaiapi.Ownership, string) (*openaiapi.Session, error) {
	if s.session == nil {
		return nil, openaiapi.ErrNotFound
	}
	x := *s.session
	return &x, nil
}

type stubCipher struct{}

func (stubCipher) Encrypt(v string) (string, error) { return v, nil }
func (stubCipher) Decrypt(v string) (string, error) { return v, nil }

func authenticated(ctx context.Context) context.Context {
	ctx = contextkeys.WithAuthenticatedAPIKey(ctx, "tenant", "hash")
	return contextkeys.WithUserRole(ctx, "service")
}
func authInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
			return next(authenticated(ctx), r)
		}
	}
}

func newTestServer(t *testing.T, spend SpendCheck) (*Server, *openaiapi.Service) {
	t.Helper()
	svc, err := openaiapi.NewService(&stubStore{session: &openaiapi.Session{ID: "00000000-0000-0000-0000-000000000001"}}, stubCipher{}, nil, nil, nil, openaiapi.Config{AllowedModels: []string{"gpt-6"}, ExecutorVersion: "test-executor"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close(context.Background()) })
	return NewServer(svc, spend), svc
}

func TestConnectJSONBinaryAndNativeGRPCProtocols(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	path, handler := srv.RegisterConnectServer(connect.WithInterceptors(authInterceptor()))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	h := httptest.NewUnstartedServer(mux)
	h.EnableHTTP2 = true
	h.StartTLS()
	defer h.Close()
	for _, tc := range []struct {
		name string
		opts []connect.ClientOption
	}{{"connect binary", nil}, {"connect json", []connect.ClientOption{connect.WithProtoJSON()}}, {"grpc over http2", []connect.ClientOption{connect.WithGRPC()}}} {
		t.Run(tc.name, func(t *testing.T) {
			client := openaiagentsconnect.NewOpenAIAgentsServiceClient(h.Client(), h.URL, tc.opts...)
			resp, err := client.GetCapabilities(context.Background(), connect.NewRequest(&pb.GetCapabilitiesRequest{}))
			if err != nil {
				t.Fatal(err)
			}
			if !resp.Msg.GetCapabilities().GetEnabled() || resp.Msg.GetCapabilities().GetExecutorVersion() != "test-executor" {
				t.Fatalf("capabilities=%+v", resp.Msg.GetCapabilities())
			}
		})
	}
}

func TestSpendGateBlocksPaidProceduresBeforeMutation(t *testing.T) {
	srv, _ := newTestServer(t, func() (bool, string) { return true, "spend limit reached" })
	path, handler := srv.RegisterConnectServer(connect.WithInterceptors(authInterceptor()))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	h := httptest.NewServer(mux)
	defer h.Close()
	client := openaiagentsconnect.NewOpenAIAgentsServiceClient(h.Client(), h.URL)
	_, err := client.CreateSession(context.Background(), connect.NewRequest(&pb.CreateSessionRequest{}))
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("code=%v err=%v", connect.CodeOf(err), err)
	}
}

func TestApprovalSpendGateBlocksApprovalButAllowsDenial(t *testing.T) {
	srv, _ := newTestServer(t, func() (bool, string) { return true, "spend limit reached" })
	path, handler := srv.RegisterConnectServer(connect.WithInterceptors(authInterceptor()))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	h := httptest.NewServer(mux)
	defer h.Close()
	client := openaiagentsconnect.NewOpenAIAgentsServiceClient(h.Client(), h.URL)
	_, approvedErr := client.ResolveApproval(context.Background(), connect.NewRequest(&pb.ResolveApprovalRequest{ApprovalId: "00000000-0000-0000-0000-000000000001", Decision: "approved"}))
	if connect.CodeOf(approvedErr) != connect.CodeResourceExhausted {
		t.Fatalf("approved code=%v", connect.CodeOf(approvedErr))
	}
	_, deniedErr := client.ResolveApproval(context.Background(), connect.NewRequest(&pb.ResolveApprovalRequest{ApprovalId: "00000000-0000-0000-0000-000000000001", Decision: "denied"}))
	if connect.CodeOf(deniedErr) == connect.CodeResourceExhausted {
		t.Fatalf("denial was spend-blocked: %v", deniedErr)
	}
}

func TestAuthenticationPrecedesSpendGate(t *testing.T) {
	srv, _ := newTestServer(t, func() (bool, string) { return true, "private spend state" })
	path, handler := srv.RegisterConnectServer(connect.WithInterceptors(headerAuthInterceptor()))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	h := httptest.NewServer(mux)
	defer h.Close()
	client := openaiagentsconnect.NewOpenAIAgentsServiceClient(h.Client(), h.URL)
	_, err := client.CreateSession(context.Background(), connect.NewRequest(&pb.CreateSessionRequest{}))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("code=%v err=%v", connect.CodeOf(err), err)
	}
}

func TestGeneratedGRPCGoClientOverHTTP2WithAuthorizationMetadata(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	path, handler := srv.RegisterConnectServer(connect.WithInterceptors(headerAuthInterceptor()))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	h := httptest.NewUnstartedServer(mux)
	h.EnableHTTP2 = true
	h.StartTLS()
	defer h.Close()
	conn, err := grpc.NewClient(strings.TrimPrefix(h.URL, "https://"), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{InsecureSkipVerify: true})))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer fixture-token"))
	resp, err := pb.NewOpenAIAgentsServiceClient(conn).GetCapabilities(ctx, &pb.GetCapabilitiesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.GetCapabilities().GetEnabled() {
		t.Fatal("capabilities disabled")
	}
}

func TestOversizedRPCMessageRejected(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	path, handler := srv.RegisterConnectServer(connect.WithInterceptors(authInterceptor()))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	h := httptest.NewServer(mux)
	defer h.Close()
	client := openaiagentsconnect.NewOpenAIAgentsServiceClient(h.Client(), h.URL)
	_, err := client.UploadSessionInput(context.Background(), connect.NewRequest(&pb.UploadSessionInputRequest{SessionId: "00000000-0000-0000-0000-000000000001", Filename: "large", Content: make([]byte, maxReadBytes+1)}))
	if err == nil {
		t.Fatal("oversized request accepted")
	}
}

func TestUpdateConnectionRejectsUnknownMaskBeforeStoreAccess(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	_, err := srv.UpdateConnection(authenticated(context.Background()), connect.NewRequest(&pb.UpdateConnectionRequest{ConnectionId: "00000000-0000-0000-0000-000000000001", UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"credential_ref"}}}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code=%v err=%v", connect.CodeOf(err), err)
	}
}

func TestConnectionPatchPresenceAndClearSemantics(t *testing.T) {
	p, err := connectionPatch(&pb.UpdateConnectionRequest{AllowedModels: nil, ExecutorKey: proto.String(""), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"allowed_models", "executor_key"}}})
	if err != nil {
		t.Fatal(err)
	}
	if p.AllowedModels == nil || len(*p.AllowedModels) != 0 || p.ExecutorKey == nil || *p.ExecutorKey != "" {
		t.Fatalf("patch=%+v", p)
	}
	if p.APIKey != nil || p.ProjectID != nil || p.Name != nil {
		t.Fatalf("omitted fields became present: %+v", p)
	}
}

func TestSendInputMappingPreservesIdempotencyAndTypedJSON(t *testing.T) {
	v, err := structpb.NewValue(map[string]any{"role": "user", "content": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := sendInputRequest(&pb.SendInputRequest{IdempotencyKey: "idem-123", Input: v})
	if err != nil {
		t.Fatal(err)
	}
	if mapped.IdempotencyKey != "idem-123" || string(mapped.Input) != `{"content":"hello","role":"user"}` {
		t.Fatalf("mapped=%+v", mapped)
	}
}

func TestUsagePreservesUnavailableRatherThanReportedZero(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	resp, err := srv.GetSessionUsage(authenticated(context.Background()), connect.NewRequest(&pb.GetSessionUsageRequest{SessionId: "00000000-0000-0000-0000-000000000001"}))
	if err != nil {
		t.Fatal(err)
	}
	u := resp.Msg.GetUsage()
	if u.GetStatus() != "unavailable" || u.InputTokens != nil || u.OutputTokens != nil || u.TotalTokens != nil {
		t.Fatalf("usage=%+v", u)
	}
}

func TestOwnershipRequiresVerifiedMatchingTenantScope(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	if _, err := srv.GetCapabilities(context.Background(), connect.NewRequest(&pb.GetCapabilitiesRequest{})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("unauthenticated code=%v err=%v", connect.CodeOf(err), err)
	}
	ctx := contextkeys.WithAuthenticatedAPIKey(context.Background(), "tenant-a", "hash")
	ctx = contextkeys.WithRequestInstanceScope(ctx, contextkeys.RequestInstanceScope{OrganizationID: "org-b", InstanceID: "instance-b"})
	if _, err := srv.GetCapabilities(ctx, connect.NewRequest(&pb.GetCapabilitiesRequest{})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("foreign-scope code=%v err=%v", connect.CodeOf(err), err)
	}
}

func TestRPCErrorSanitizesProviderSecrets(t *testing.T) {
	err := rpcError(errors.New("request failed for sk-secret at https://private.example/path"))
	if message := err.Error(); strings.Contains(message, "sk-secret") || strings.Contains(message, "private.example") {
		t.Fatalf("RPC error leaked sensitive provider data: %s", message)
	}
}

func TestNativeContractCoversCurrentRESTOperations(t *testing.T) {
	want := []string{"GetCapabilities", "CreateConnection", "ListConnections", "GetConnection", "UpdateConnection", "DeleteConnection", "CreateSession", "ListSessions", "GetSession", "DeleteSession", "SendInput", "CancelSession", "ListSessionEvents", "GetSessionUsage", "ListApprovals", "ResolveApproval", "UploadSessionInput", "ListSessionArtifacts", "ExportSessionArtifact", "GetArtifactDownload", "DeleteSessionArtifact"}
	methods := pb.File_everstack_openai_agents_v1_openai_agents_service_proto.Services().ByName("OpenAIAgentsService").Methods()
	got := make([]string, methods.Len())
	for i := range got {
		got[i] = string(methods.Get(i).Name())
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("native RPCs=%v, want current REST parity %v", got, want)
	}
}

func TestEventCursorRemainsLosslessPastJSONSafeInteger(t *testing.T) {
	store := &stubStore{session: &openaiapi.Session{ID: "00000000-0000-0000-0000-000000000001"}, events: []openaiapi.Event{{Cursor: 1<<53 + 17, EventID: "event", Type: "turn"}}}
	svc, _ := openaiapi.NewService(store, stubCipher{}, nil, nil, nil, openaiapi.Config{})
	t.Cleanup(func() { _ = svc.Close(context.Background()) })
	srv := NewServer(svc, nil)
	resp, err := srv.ListSessionEvents(authenticated(context.Background()), connect.NewRequest(&pb.ListSessionEventsRequest{SessionId: "00000000-0000-0000-0000-000000000001"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Msg.Events[0].Cursor; got != 1<<53+17 {
		t.Fatalf("cursor=%d", got)
	}
}

func TestViewerCannotDeleteArtifact(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	ctx := contextkeys.WithTenantID(context.Background(), "tenant")
	ctx = contextkeys.WithTenantAuthenticated(ctx)
	ctx = contextkeys.WithUserID(ctx, "viewer")
	ctx = contextkeys.WithUserRole(ctx, "viewer")
	_, err := srv.DeleteSessionArtifact(ctx, connect.NewRequest(&pb.DeleteSessionArtifactRequest{SessionId: "00000000-0000-0000-0000-000000000001", ObjectId: "00000000-0000-0000-0000-000000000002"}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code=%v err=%v", connect.CodeOf(err), err)
	}
}
