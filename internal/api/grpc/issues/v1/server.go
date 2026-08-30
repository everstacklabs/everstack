// Package issues is the ConnectRPC handler for the Issues (error-tracking)
// service. It is a thin transport layer over internal/issues: extract the
// tenant from context, delegate, and map domain types to proto.
package issues

import (
	"context"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"
	issuepkg "github.com/everstacklabs/everstack/internal/issues"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	issuespb "github.com/everstacklabs/everstack/pkg/grpc/everstack/issues/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/issues/v1/issuesconnect"
	gwruntime "github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// requireTenantID enforces the tenant-from-context contract (never req.Msg).
func requireTenantID(ctx context.Context) (string, error) {
	if tid := contextkeys.GetTenantID(ctx); tid != "" {
		return tid, nil
	}
	return "", connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
}

// Server implements the IssuesService gRPC server.
type Server struct {
	ctx                 context.Context
	svc                 *issuepkg.Service
	serviceInterceptors []connect.Interceptor
}

func CreateServer(ctx context.Context, svc *issuepkg.Service) *Server {
	return &Server{ctx: ctx, svc: svc}
}

func (s *Server) WithInterceptors(interceptors ...connect.Interceptor) *Server {
	s.serviceInterceptors = append(s.serviceInterceptors, interceptors...)
	return s
}

func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	all := make([]connect.Interceptor, 0, len(s.serviceInterceptors)+len(interceptors))
	all = append(all, s.serviceInterceptors...)
	all = append(all, interceptors...)
	return issuesconnect.NewIssuesServiceHandler(s, connect.WithInterceptors(all...))
}

func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return issuespb.File_everstack_issues_v1_issues_service_proto
}

func (s *Server) AppName() string      { return issuesconnect.IssuesServiceName }
func (s *Server) MethodPrefix() string { return issuesconnect.IssuesServiceName }

func (s *Server) RegisterGateway(_ context.Context, _ *gwruntime.ServeMux, _ string, _ []grpc.DialOption) error {
	return nil
}

// ─── RPCs ────────────────────────────────────────────────────────────

func (s *Server) ListIssues(ctx context.Context, req *connect.Request[issuespb.ListIssuesRequest]) (*connect.Response[issuespb.ListIssuesResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	from, to := windowFromReq(req.Msg.GetFrom(), req.Msg.GetTo())
	issues, err := s.svc.ListIssues(ctx, tenantID, issuepkg.ListParams{
		From:         from,
		To:           to,
		StatusFilter: statusToString(req.Msg.GetStatusFilter()),
		Query:        req.Msg.GetQuery(),
		Limit:        int(req.Msg.GetLimit()),
		Offset:       int(req.Msg.GetOffset()),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*issuespb.Issue, 0, len(issues))
	for i := range issues {
		out = append(out, toProtoIssue(issues[i]))
	}
	return connect.NewResponse(&issuespb.ListIssuesResponse{Issues: out, Total: int32(len(out))}), nil
}

func (s *Server) GetIssue(ctx context.Context, req *connect.Request[issuespb.GetIssueRequest]) (*connect.Response[issuespb.GetIssueResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	if req.Msg.GetFingerprint() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("fingerprint required"))
	}
	from, to := windowFromReq(req.Msg.GetFrom(), req.Msg.GetTo())
	detail, err := s.svc.GetIssue(ctx, tenantID, req.Msg.GetFingerprint(), from, to, req.Msg.GetInterval())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	occ := make([]*issuespb.Occurrence, 0, len(detail.Occurrences))
	for _, o := range detail.Occurrences {
		occ = append(occ, &issuespb.Occurrence{
			TraceId:   o.TraceID,
			SpanId:    o.SpanID,
			SpanName:  o.SpanName,
			Timestamp: timestamppb.New(o.Timestamp),
			Message:   o.Message,
		})
	}
	trend := make([]*issuespb.TrendPoint, 0, len(detail.Trend))
	for _, t := range detail.Trend {
		trend = append(trend, &issuespb.TrendPoint{Timestamp: timestamppb.New(t.Timestamp), Count: t.Count})
	}

	var latest *issuespb.EventDetail
	if detail.LatestEvent != nil {
		latest = &issuespb.EventDetail{
			TraceId:    detail.LatestEvent.TraceID,
			SpanId:     detail.LatestEvent.SpanID,
			Timestamp:  timestamppb.New(detail.LatestEvent.Timestamp),
			Message:    detail.LatestEvent.Message,
			Attributes: detail.LatestEvent.Attributes,
		}
	}
	crumbs := make([]*issuespb.SpanCrumb, 0, len(detail.Breadcrumbs))
	for _, c := range detail.Breadcrumbs {
		crumbs = append(crumbs, &issuespb.SpanCrumb{
			SpanId:        c.SpanID,
			ParentSpanId:  c.ParentSpanID,
			Name:          c.Name,
			StatusCode:    c.StatusCode,
			StatusMessage: c.StatusMessage,
			Timestamp:     timestamppb.New(c.Timestamp),
			DurationMs:    c.DurationMs,
		})
	}
	tags := make([]*issuespb.TagDistribution, 0, len(detail.Tags))
	for _, d := range detail.Tags {
		vals := make([]*issuespb.TagValue, 0, len(d.Values))
		for _, v := range d.Values {
			vals = append(vals, &issuespb.TagValue{Value: v.Value, Count: v.Count})
		}
		tags = append(tags, &issuespb.TagDistribution{Key: d.Key, Total: d.Total, Values: vals})
	}
	activity := make([]*issuespb.IssueActivity, 0, len(detail.Activity))
	for _, a := range detail.Activity {
		activity = append(activity, &issuespb.IssueActivity{
			Actor:      a.Actor,
			Action:     a.Action,
			FromStatus: a.FromStatus,
			ToStatus:   a.ToStatus,
			Note:       a.Note,
			CreatedAt:  timestamppb.New(a.CreatedAt),
		})
	}

	return connect.NewResponse(&issuespb.GetIssueResponse{
		Issue:       toProtoIssue(detail.Issue),
		Occurrences: occ,
		Trend:       trend,
		LatestEvent: latest,
		Breadcrumbs: crumbs,
		Tags:        tags,
		Users:       detail.Users,
		Activity:    activity,
	}), nil
}

func (s *Server) UpdateIssueStatus(ctx context.Context, req *connect.Request[issuespb.UpdateIssueStatusRequest]) (*connect.Response[issuespb.UpdateIssueStatusResponse], error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	if req.Msg.GetFingerprint() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("fingerprint required"))
	}
	status := statusToString(req.Msg.GetStatus())
	if status == "" || status == issuepkg.StatusRegressed {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("status must be unresolved, resolved or ignored"))
	}
	st := &issuepkg.IssueState{
		Fingerprint: req.Msg.GetFingerprint(),
		Status:      status,
		Signature:   req.Msg.GetSignature(),
		Title:       req.Msg.GetTitle(),
	}
	if req.Msg.Assignee != nil {
		st.Assignee = req.Msg.Assignee
	}
	if req.Msg.GetSnoozeUntil() != nil {
		t := req.Msg.GetSnoozeUntil().AsTime()
		st.SnoozeUntil = &t
	}
	iss, err := s.svc.UpdateStatus(ctx, tenantID, contextkeys.GetUserID(ctx), st)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&issuespb.UpdateIssueStatusResponse{Issue: toProtoIssue(*iss)}), nil
}

// ─── mapping helpers ─────────────────────────────────────────────────

// windowFromReq defaults to the last 24h when the request omits a window.
func windowFromReq(from, to *timestamppb.Timestamp) (time.Time, time.Time) {
	end := time.Now().UTC()
	if to != nil {
		end = to.AsTime()
	}
	start := end.Add(-24 * time.Hour)
	if from != nil {
		start = from.AsTime()
	}
	return start, end
}

func toProtoIssue(i issuepkg.Issue) *issuespb.Issue {
	out := &issuespb.Issue{
		Fingerprint:    i.Fingerprint,
		Title:          i.Title,
		Signature:      i.Signature,
		Category:       categoryToProto(i.Category),
		Status:         statusToProto(i.Status),
		Count:          i.Count,
		Provider:       i.Provider,
		Model:          i.Model,
		SampleTraceIds: i.SampleTraceIDs,
		Assignee:       i.Assignee,
		Sparkline:      i.Spark,
	}
	if !i.FirstSeen.IsZero() {
		out.FirstSeen = timestamppb.New(i.FirstSeen)
	}
	if !i.LastSeen.IsZero() {
		out.LastSeen = timestamppb.New(i.LastSeen)
	}
	if i.ResolvedAt != nil {
		out.ResolvedAt = timestamppb.New(*i.ResolvedAt)
	}
	if i.SnoozeUntil != nil {
		out.SnoozeUntil = timestamppb.New(*i.SnoozeUntil)
	}
	return out
}

func statusToProto(s string) issuespb.IssueStatus {
	switch s {
	case issuepkg.StatusUnresolved:
		return issuespb.IssueStatus_ISSUE_STATUS_UNRESOLVED
	case issuepkg.StatusResolved:
		return issuespb.IssueStatus_ISSUE_STATUS_RESOLVED
	case issuepkg.StatusIgnored:
		return issuespb.IssueStatus_ISSUE_STATUS_IGNORED
	case issuepkg.StatusRegressed:
		return issuespb.IssueStatus_ISSUE_STATUS_REGRESSED
	default:
		return issuespb.IssueStatus_ISSUE_STATUS_UNSPECIFIED
	}
}

func statusToString(s issuespb.IssueStatus) string {
	switch s {
	case issuespb.IssueStatus_ISSUE_STATUS_UNRESOLVED:
		return issuepkg.StatusUnresolved
	case issuespb.IssueStatus_ISSUE_STATUS_RESOLVED:
		return issuepkg.StatusResolved
	case issuespb.IssueStatus_ISSUE_STATUS_IGNORED:
		return issuepkg.StatusIgnored
	case issuespb.IssueStatus_ISSUE_STATUS_REGRESSED:
		return issuepkg.StatusRegressed
	default:
		return ""
	}
}

func categoryToProto(c string) issuespb.IssueCategory {
	switch c {
	case "rate_limit":
		return issuespb.IssueCategory_ISSUE_CATEGORY_RATE_LIMIT
	case "context_length":
		return issuespb.IssueCategory_ISSUE_CATEGORY_CONTEXT_LENGTH
	case "provider_5xx":
		return issuespb.IssueCategory_ISSUE_CATEGORY_PROVIDER_5XX
	case "guardrail_block":
		return issuespb.IssueCategory_ISSUE_CATEGORY_GUARDRAIL_BLOCK
	case "tool_error":
		return issuespb.IssueCategory_ISSUE_CATEGORY_TOOL_ERROR
	case "timeout":
		return issuespb.IssueCategory_ISSUE_CATEGORY_TIMEOUT
	case "auth":
		return issuespb.IssueCategory_ISSUE_CATEGORY_AUTH
	case "parse_error":
		return issuespb.IssueCategory_ISSUE_CATEGORY_PARSE_ERROR
	case "other":
		return issuespb.IssueCategory_ISSUE_CATEGORY_OTHER
	default:
		return issuespb.IssueCategory_ISSUE_CATEGORY_UNSPECIFIED
	}
}
