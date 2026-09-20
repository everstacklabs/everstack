package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/agents/openaiapi"
	"github.com/everstacklabs/everstack/internal/storage"
	pb "github.com/everstacklabs/everstack/pkg/grpc/everstack/openai_agents/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/openai_agents/v1/openaiagentsconnect"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const maxReadBytes = 2 << 20

type SpendCheck func() (bool, string)

type Server struct {
	service *openaiapi.Service
	spend   SpendCheck
}

func NewServer(service *openaiapi.Service, spend SpendCheck) *Server {
	return &Server{service: service, spend: spend}
}

func (s *Server) RegisterConnectServer(opts ...connect.HandlerOption) (string, http.Handler) {
	// Authentication and the other global interceptors must run before the
	// spend check so an unauthenticated caller cannot probe account state.
	all := append(append([]connect.HandlerOption(nil), opts...), connect.WithInterceptors(s.spendInterceptor()))
	return openaiagentsconnect.NewOpenAIAgentsServiceHandler(s, connect.WithHandlerOptions(all...), connect.WithReadMaxBytes(maxReadBytes))
}
func (*Server) FileDescriptor() protoreflect.FileDescriptor {
	return pb.File_everstack_openai_agents_v1_openai_agents_service_proto
}
func (*Server) AppName() string      { return openaiagentsconnect.OpenAIAgentsServiceName }
func (*Server) MethodPrefix() string { return openaiagentsconnect.OpenAIAgentsServiceName }

func (s *Server) spendInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			paid := req.Spec().Procedure == openaiagentsconnect.OpenAIAgentsServiceCreateSessionProcedure || req.Spec().Procedure == openaiagentsconnect.OpenAIAgentsServiceSendInputProcedure
			if req.Spec().Procedure == openaiagentsconnect.OpenAIAgentsServiceResolveApprovalProcedure {
				if r, ok := req.Any().(*pb.ResolveApprovalRequest); ok {
					paid = r.GetDecision() == "approved"
				}
			}
			if paid && s.spend != nil {
				if blocked, reason := s.spend(); blocked {
					return nil, connect.NewError(connect.CodeResourceExhausted, errors.New(reason))
				}
			}
			return next(ctx, req)
		}
	}
}

func owner(ctx context.Context) (openaiapi.Ownership, error) {
	o, err := openaiapi.OwnershipFromContext(ctx)
	if err != nil {
		return o, rpcError(err)
	}
	return o, nil
}

func rpcError(err error) error {
	if err == nil {
		return nil
	}
	code := connect.CodeInternal
	var quotaExceeded *storage.QuotaExceededError
	switch {
	case errors.Is(err, openaiapi.ErrForbidden):
		code = connect.CodePermissionDenied
	case errors.Is(err, openaiapi.ErrNotFound):
		code = connect.CodeNotFound
	case errors.Is(err, openaiapi.ErrInvalid):
		code = connect.CodeInvalidArgument
	case errors.Is(err, openaiapi.ErrUnavailable):
		code = connect.CodeUnavailable
	case errors.Is(err, openaiapi.ErrCreatePending):
		code = connect.CodeFailedPrecondition
	case errors.Is(err, openaiapi.ErrConnectionInUse):
		code = connect.CodeFailedPrecondition
	case storage.IsCode(err, storage.ErrorUnauthorized):
		code = connect.CodePermissionDenied
	case errors.As(err, &quotaExceeded):
		code = connect.CodeResourceExhausted
	case storage.IsCode(err, storage.ErrorInvalidArgument), storage.IsCode(err, storage.ErrorChecksumMismatch):
		code = connect.CodeInvalidArgument
	case storage.IsCode(err, storage.ErrorNotFound):
		code = connect.CodeNotFound
	case storage.IsCode(err, storage.ErrorAlreadyExists):
		code = connect.CodeAlreadyExists
	case storage.IsCode(err, storage.ErrorConflict):
		code = connect.CodeAborted
	case storage.IsCode(err, storage.ErrorThrottled), storage.IsCode(err, storage.ErrorUnavailable), storage.IsCode(err, storage.ErrorTimeout):
		code = connect.CodeUnavailable
	}
	return connect.NewError(code, errors.New(openaiapi.SanitizeError(err.Error())))
}

func valueToRaw(v *structpb.Value) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v.AsInterface())
	if err != nil {
		return nil, openaiapi.ErrInvalid
	}
	return b, nil
}
func rawToValue(raw json.RawMessage) (*structpb.Value, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var x any
	if err := json.Unmarshal(raw, &x); err != nil {
		return nil, err
	}
	return structpb.NewValue(x)
}
func connection(x *openaiapi.Connection) *pb.Connection {
	if x == nil {
		return nil
	}
	return &pb.Connection{Id: x.ID, Name: x.Name, Provider: x.Provider, ProjectId: x.ProjectID, OrganizationId: x.OpenAIOrganization, PrincipalId: x.OpenAIPrincipal, AllowedModels: append([]string(nil), x.AllowedModels...), Status: x.Status, LastError: x.LastError, CreatedAt: timestamppb.New(x.CreatedAt), UpdatedAt: timestamppb.New(x.UpdatedAt)}
}
func session(x *openaiapi.Session) *pb.Session {
	if x == nil {
		return nil
	}
	return &pb.Session{Id: x.ID, Provider: x.Provider, ConnectionId: x.ConnectionID, ConversationId: x.ConversationID, UpstreamSessionId: x.UpstreamSessionID, UpstreamEnvironmentId: x.UpstreamEnvironmentID, SandboxId: x.SandboxID, Model: x.Model, Environment: &pb.EnvironmentSpec{Type: x.Environment.Type, WorkspaceDirectory: x.Environment.WorkspaceDirectory}, Status: x.Status, Readiness: x.Readiness, CleanupStatus: x.CleanupStatus, EnvironmentCleanupStatus: x.EnvironmentCleanupStatus, LastError: x.LastError, CreatedAt: timestamppb.New(x.CreatedAt), UpdatedAt: timestamppb.New(x.UpdatedAt)}
}
func artifact(x *openaiapi.ArtifactDTO) *pb.Artifact {
	if x == nil {
		return nil
	}
	return &pb.Artifact{ObjectId: x.ObjectID, Filename: x.Filename, ContentType: x.ContentType, SizeBytes: x.SizeBytes, Sha256: x.SHA256, Kind: x.Kind, CreatedAt: timestamppb.New(x.CreatedAt)}
}

func (s *Server) GetCapabilities(ctx context.Context, _ *connect.Request[pb.GetCapabilitiesRequest]) (*connect.Response[pb.GetCapabilitiesResponse], error) {
	o, e := owner(ctx)
	if e != nil {
		return nil, e
	}
	x, e := s.service.Capabilities(ctx, o)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&pb.GetCapabilitiesResponse{Capabilities: &pb.Capabilities{Enabled: x.Enabled, Provider: x.Provider, EnvironmentModes: x.EnvironmentModes, ToolTypes: x.ToolTypes, FileOperations: x.FileOperations, ExecutorVersion: x.ExecutorVersion, Readiness: x.Readiness, CanManageConnections: x.CanManageConnections, CanRunSessions: x.CanRunSessions, CanResolveApprovals: x.CanResolveApprovals}}), nil
}
func (s *Server) CreateConnection(ctx context.Context, r *connect.Request[pb.CreateConnectionRequest]) (*connect.Response[pb.CreateConnectionResponse], error) {
	o, e := owner(ctx)
	if e != nil {
		return nil, e
	}
	m := r.Msg
	x, e := s.service.CreateConnection(ctx, o, openaiapi.CreateConnectionRequest{Name: m.Name, APIKey: m.ApiKey, ExecutorKey: m.ExecutorKey, ProjectID: m.ProjectId, OrganizationID: m.OrganizationId, PrincipalID: m.PrincipalId, AllowedModels: m.AllowedModels})
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&pb.CreateConnectionResponse{Connection: connection(x)}), nil
}
func (s *Server) ListConnections(ctx context.Context, _ *connect.Request[pb.ListConnectionsRequest]) (*connect.Response[pb.ListConnectionsResponse], error) {
	o, e := owner(ctx)
	if e != nil {
		return nil, e
	}
	xs, e := s.service.ListConnections(ctx, o)
	if e != nil {
		return nil, rpcError(e)
	}
	out := make([]*pb.Connection, len(xs))
	for i := range xs {
		out[i] = connection(&xs[i])
	}
	return connect.NewResponse(&pb.ListConnectionsResponse{Connections: out}), nil
}
func (s *Server) GetConnection(ctx context.Context, r *connect.Request[pb.GetConnectionRequest]) (*connect.Response[pb.GetConnectionResponse], error) {
	o, e := owner(ctx)
	if e != nil {
		return nil, e
	}
	x, e := s.service.GetConnection(ctx, o, r.Msg.ConnectionId)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&pb.GetConnectionResponse{Connection: connection(x)}), nil
}
func (s *Server) UpdateConnection(ctx context.Context, r *connect.Request[pb.UpdateConnectionRequest]) (*connect.Response[pb.UpdateConnectionResponse], error) {
	o, e := owner(ctx)
	if e != nil {
		return nil, e
	}
	m := r.Msg
	if m.UpdateMask == nil || len(m.UpdateMask.Paths) == 0 {
		return nil, rpcError(openaiapi.ErrInvalid)
	}
	u, e := connectionPatch(m)
	if e != nil {
		return nil, rpcError(e)
	}
	x, e := s.service.UpdateConnection(ctx, o, m.ConnectionId, u)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&pb.UpdateConnectionResponse{Connection: connection(x)}), nil
}

func connectionPatch(m *pb.UpdateConnectionRequest) (openaiapi.UpdateConnectionRequest, error) {
	allowed := []string{"name", "api_key", "executor_key", "project_id", "organization_id", "principal_id", "allowed_models"}
	u := openaiapi.UpdateConnectionRequest{}
	for _, p := range m.UpdateMask.Paths {
		if !slices.Contains(allowed, p) {
			return u, fmt.Errorf("unknown update path %q: %w", p, openaiapi.ErrInvalid)
		}
		switch p {
		case "name":
			v := m.GetName()
			u.Name = &v
		case "api_key":
			v := m.GetApiKey()
			u.APIKey = &v
		case "executor_key":
			v := m.GetExecutorKey()
			u.ExecutorKey = &v
		case "project_id":
			v := m.GetProjectId()
			u.ProjectID = &v
		case "organization_id":
			v := m.GetOrganizationId()
			u.OrganizationID = &v
		case "principal_id":
			v := m.GetPrincipalId()
			u.PrincipalID = &v
		case "allowed_models":
			v := append([]string(nil), m.AllowedModels...)
			u.AllowedModels = &v
		}
	}
	return u, nil
}
func (s *Server) DeleteConnection(ctx context.Context, r *connect.Request[pb.DeleteConnectionRequest]) (*connect.Response[pb.DeleteConnectionResponse], error) {
	o, e := owner(ctx)
	if e != nil {
		return nil, e
	}
	if e = s.service.DeleteConnection(ctx, o, r.Msg.ConnectionId); e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&pb.DeleteConnectionResponse{Deleted: true}), nil
}

func (s *Server) CreateSession(ctx context.Context, r *connect.Request[pb.CreateSessionRequest]) (*connect.Response[pb.CreateSessionResponse], error) {
	o, e := owner(ctx)
	if e != nil {
		return nil, e
	}
	m := r.Msg
	input, e := valueToRaw(m.Input)
	if e != nil {
		return nil, rpcError(e)
	}
	tools := make([]openaiapi.ToolSpec, len(m.Tools))
	for i, t := range m.Tools {
		tools[i] = openaiapi.ToolSpec{Type: t.Type, ResourceID: t.ResourceId, Name: t.Name}
	}
	env := openaiapi.EnvironmentSpec{}
	if m.Environment != nil {
		env.Type = m.Environment.Type
		env.WorkspaceDirectory = m.Environment.WorkspaceDirectory
	}
	x, e := s.service.CreateSession(ctx, o, openaiapi.CreateSessionRequest{ConnectionID: m.ConnectionId, IdempotencyKey: m.IdempotencyKey, Model: m.Model, Instructions: m.Instructions, Environment: env, Input: input, Tools: tools})
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&pb.CreateSessionResponse{Session: session(x)}), nil
}
func (s *Server) ListSessions(ctx context.Context, _ *connect.Request[pb.ListSessionsRequest]) (*connect.Response[pb.ListSessionsResponse], error) {
	o, e := owner(ctx)
	if e != nil {
		return nil, e
	}
	xs, e := s.service.ListSessions(ctx, o)
	if e != nil {
		return nil, rpcError(e)
	}
	out := make([]*pb.Session, len(xs))
	for i := range xs {
		out[i] = session(&xs[i])
	}
	return connect.NewResponse(&pb.ListSessionsResponse{Sessions: out}), nil
}
func (s *Server) GetSession(ctx context.Context, r *connect.Request[pb.GetSessionRequest]) (*connect.Response[pb.GetSessionResponse], error) {
	o, e := owner(ctx)
	if e != nil {
		return nil, e
	}
	x, e := s.service.GetSession(ctx, o, r.Msg.SessionId)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&pb.GetSessionResponse{Session: session(x)}), nil
}
func (s *Server) DeleteSession(ctx context.Context, r *connect.Request[pb.DeleteSessionRequest]) (*connect.Response[pb.DeleteSessionResponse], error) {
	o, e := owner(ctx)
	if e != nil {
		return nil, e
	}
	if e = s.service.DeleteSession(ctx, o, r.Msg.SessionId); e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&pb.DeleteSessionResponse{Deleted: true}), nil
}
func (s *Server) SendInput(ctx context.Context, r *connect.Request[pb.SendInputRequest]) (*connect.Response[pb.SendInputResponse], error) {
	o, e := owner(ctx)
	if e != nil {
		return nil, e
	}
	inputRequest, e := sendInputRequest(r.Msg)
	if e != nil {
		return nil, rpcError(e)
	}
	if e = s.service.SendInput(ctx, o, r.Msg.SessionId, inputRequest); e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&pb.SendInputResponse{Accepted: true}), nil
}

func sendInputRequest(m *pb.SendInputRequest) (openaiapi.InputRequest, error) {
	input, err := valueToRaw(m.Input)
	return openaiapi.InputRequest{IdempotencyKey: m.IdempotencyKey, Input: input}, err
}
func (s *Server) CancelSession(ctx context.Context, r *connect.Request[pb.CancelSessionRequest]) (*connect.Response[pb.CancelSessionResponse], error) {
	o, e := owner(ctx)
	if e != nil {
		return nil, e
	}
	if e = s.service.Cancel(ctx, o, r.Msg.SessionId); e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&pb.CancelSessionResponse{Accepted: true}), nil
}
func (s *Server) ListSessionEvents(ctx context.Context, r *connect.Request[pb.ListSessionEventsRequest]) (*connect.Response[pb.ListSessionEventsResponse], error) {
	o, e := owner(ctx)
	if e != nil {
		return nil, e
	}
	xs, e := s.service.ListEvents(ctx, o, r.Msg.SessionId, r.Msg.AfterCursor, int(r.Msg.Limit))
	if e != nil {
		return nil, rpcError(e)
	}
	out := make([]*pb.SessionEvent, len(xs))
	next := r.Msg.AfterCursor
	for i, x := range xs {
		v, ve := rawToValue(x.Payload)
		if ve != nil {
			return nil, rpcError(ve)
		}
		out[i] = &pb.SessionEvent{Cursor: x.Cursor, EventId: x.EventID, Type: x.Type, Version: x.Version, UpstreamItemId: x.UpstreamItemID, Payload: v, ToolFailed: x.ToolFailed, CreatedAt: timestamppb.New(x.CreatedAt)}
		next = x.Cursor
	}
	return connect.NewResponse(&pb.ListSessionEventsResponse{Events: out, NextCursor: next, HasMore: r.Msg.Limit > 0 && len(xs) == int(r.Msg.Limit)}), nil
}
func (s *Server) GetSessionUsage(ctx context.Context, r *connect.Request[pb.GetSessionUsageRequest]) (*connect.Response[pb.GetSessionUsageResponse], error) {
	o, e := owner(ctx)
	if e != nil {
		return nil, e
	}
	x, e := s.service.Usage(ctx, o, r.Msg.SessionId)
	if e != nil {
		return nil, rpcError(e)
	}
	u := &pb.SessionUsage{Status: string(x.Status), InputTokens: x.InputTokens, OutputTokens: x.OutputTokens, TotalTokens: x.TotalTokens, Source: x.Source}
	if x.UpdatedAt != nil {
		u.UpdatedAt = timestamppb.New(*x.UpdatedAt)
	}
	return connect.NewResponse(&pb.GetSessionUsageResponse{Usage: u}), nil
}
func (s *Server) ListApprovals(ctx context.Context, _ *connect.Request[pb.ListApprovalsRequest]) (*connect.Response[pb.ListApprovalsResponse], error) {
	o, e := owner(ctx)
	if e != nil {
		return nil, e
	}
	xs, e := s.service.ListApprovals(ctx, o)
	if e != nil {
		return nil, rpcError(e)
	}
	out := make([]*pb.Approval, len(xs))
	for i, x := range xs {
		v, ve := rawToValue(x.Arguments)
		if ve != nil {
			return nil, rpcError(ve)
		}
		out[i] = &pb.Approval{Id: x.ID, SessionId: x.SessionID, TurnId: x.TurnID, CallId: x.CallID, ToolName: x.ToolName, ResourceKind: x.ResourceKind, ResourceId: x.ResourceID, Status: x.Status, RequestedBy: x.RequestedBy, Arguments: v, RequestedAt: timestamppb.New(x.RequestedAt), ExpiresAt: timestamppb.New(x.ExpiresAt)}
	}
	return connect.NewResponse(&pb.ListApprovalsResponse{Approvals: out}), nil
}
func (s *Server) ResolveApproval(ctx context.Context, r *connect.Request[pb.ResolveApprovalRequest]) (*connect.Response[pb.ResolveApprovalResponse], error) {
	o, e := owner(ctx)
	if e != nil {
		return nil, e
	}
	if e = s.service.ResolveApproval(ctx, o, r.Msg.ApprovalId, r.Msg.Decision); e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&pb.ResolveApprovalResponse{Resolved: true}), nil
}
func (s *Server) UploadSessionInput(ctx context.Context, r *connect.Request[pb.UploadSessionInputRequest]) (*connect.Response[pb.UploadSessionInputResponse], error) {
	o, e := owner(ctx)
	if e != nil {
		return nil, e
	}
	x, e := s.service.UploadSessionInput(ctx, o, r.Msg.SessionId, r.Msg.Filename, r.Msg.ContentType, r.Msg.Content, r.Msg.Destination)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&pb.UploadSessionInputResponse{Artifact: artifact(x)}), nil
}
func (s *Server) ListSessionArtifacts(ctx context.Context, r *connect.Request[pb.ListSessionArtifactsRequest]) (*connect.Response[pb.ListSessionArtifactsResponse], error) {
	o, e := owner(ctx)
	if e != nil {
		return nil, e
	}
	xs, e := s.service.ListSessionArtifacts(ctx, o, r.Msg.SessionId)
	if e != nil {
		return nil, rpcError(e)
	}
	out := make([]*pb.Artifact, len(xs))
	for i := range xs {
		out[i] = artifact(&xs[i])
	}
	return connect.NewResponse(&pb.ListSessionArtifactsResponse{Artifacts: out}), nil
}
func (s *Server) ExportSessionArtifact(ctx context.Context, r *connect.Request[pb.ExportSessionArtifactRequest]) (*connect.Response[pb.ExportSessionArtifactResponse], error) {
	o, e := owner(ctx)
	if e != nil {
		return nil, e
	}
	x, e := s.service.ExportSessionArtifact(ctx, o, r.Msg.SessionId, r.Msg.Path)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&pb.ExportSessionArtifactResponse{Artifact: artifact(x)}), nil
}
func (s *Server) GetArtifactDownload(ctx context.Context, r *connect.Request[pb.GetArtifactDownloadRequest]) (*connect.Response[pb.GetArtifactDownloadResponse], error) {
	o, e := owner(ctx)
	if e != nil {
		return nil, e
	}
	x, e := s.service.GetArtifactDownload(ctx, o, r.Msg.SessionId, r.Msg.ObjectId)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&pb.GetArtifactDownloadResponse{Download: &pb.ArtifactDownload{Artifact: artifact(&x.ArtifactDTO), Url: x.URL, ExpiresAt: timestamppb.New(x.ExpiresAt)}}), nil
}
func (s *Server) DeleteSessionArtifact(ctx context.Context, r *connect.Request[pb.DeleteSessionArtifactRequest]) (*connect.Response[pb.DeleteSessionArtifactResponse], error) {
	o, e := owner(ctx)
	if e != nil {
		return nil, e
	}
	if e = s.service.DeleteSessionArtifact(ctx, o, r.Msg.SessionId, r.Msg.ObjectId); e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&pb.DeleteSessionArtifactResponse{Deleted: true}), nil
}

var _ openaiagentsconnect.OpenAIAgentsServiceHandler = (*Server)(nil)
