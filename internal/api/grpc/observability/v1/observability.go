package v1

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
	traceshandler "github.com/everstacklabs/everstack/internal/query/handlers/traces"
	tracespb "github.com/everstacklabs/everstack/pkg/grpc/everstack/traces/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ============================================================================
// GetMetricsDashboard
// ============================================================================

func (s *Server) GetMetricsDashboard(
	ctx context.Context,
	req *connect.Request[tracespb.GetMetricsDashboardRequest],
) (*connect.Response[tracespb.GetMetricsDashboardResponse], error) {
	sys, err := s.getSystem(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	filter := req.Msg.GetFilter()
	startTime, endTime := parseFilterTimes(filter)

	var models, providers, environments []string
	if filter != nil {
		models = filter.GetModels()
		providers = filter.GetProviders()
		environments = filter.GetEnvironments()
	}

	dashQuery := traceshandler.NewMetricsDashboardQuery(startTime, endTime, models, providers, environments, req.Msg.GetCompare())

	result, err := sys.QueryBus.Execute(ctx, dashQuery)
	if err != nil {
		logger.WithError(err).Error("failed to get metrics dashboard")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	response, ok := result.(*query.Response)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid response type"))
	}

	resp := &tracespb.GetMetricsDashboardResponse{}
	switch dashboard := response.Data.(type) {
	case traceshandler.MetricsDashboardResult:
		resp.Dashboard = metricsDashboardToProto(dashboard)
	case traceshandler.MetricsDashboardCompareResult:
		resp.Dashboard = metricsDashboardToProto(dashboard.Current)
		resp.Previous = metricsDashboardToProto(dashboard.Previous)
		resp.Deltas = metricsDeltasToProto(dashboard.Current, dashboard.Previous)
	default:
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid data type"))
	}

	return connect.NewResponse(resp), nil
}

// ============================================================================
// GetMetricsTimeSeries
// ============================================================================

func (s *Server) GetMetricsTimeSeries(
	ctx context.Context,
	req *connect.Request[tracespb.GetMetricsTimeSeriesRequest],
) (*connect.Response[tracespb.GetMetricsTimeSeriesResponse], error) {
	sys, err := s.getSystem(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	filter := req.Msg.GetFilter()
	startTime, endTime := parseFilterTimes(filter)

	var models, providers, environments []string
	if filter != nil {
		models = filter.GetModels()
		providers = filter.GetProviders()
		environments = filter.GetEnvironments()
	}

	metric := req.Msg.GetMetric()
	if metric == "" {
		metric = "request_count"
	}

	granularity := normalizeGranularity(req.Msg.GetGranularity())

	tsQuery := traceshandler.NewMetricsTimeSeriesQuery(
		startTime, endTime,
		metric,
		req.Msg.GetGroupBy(),
		granularity,
		models, providers, environments,
		req.Msg.GetCompare(),
	)

	result, err := sys.QueryBus.Execute(ctx, tsQuery)
	if err != nil {
		logger.WithError(err).Error("failed to get metrics time series")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	response, ok := result.(*query.Response)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid response type"))
	}

	resp := &tracespb.GetMetricsTimeSeriesResponse{}
	switch seriesData := response.Data.(type) {
	case []traceshandler.MetricsTimeSeriesResult:
		resp.Series = metricsTimeSeriesToProto(seriesData)
	case traceshandler.MetricsTimeSeriesCompareResult:
		resp.Series = metricsTimeSeriesToProto(seriesData.Current)
		resp.PreviousSeries = metricsTimeSeriesToProto(seriesData.Previous)
	default:
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid data type"))
	}

	return connect.NewResponse(resp), nil
}

// ============================================================================
// GetMetricsBreakdown
// ============================================================================

func (s *Server) GetMetricsBreakdown(
	ctx context.Context,
	req *connect.Request[tracespb.GetMetricsBreakdownRequest],
) (*connect.Response[tracespb.GetMetricsBreakdownResponse], error) {
	sys, err := s.getSystem(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	filter := req.Msg.GetFilter()
	startTime, endTime := parseFilterTimes(filter)

	var models, providers, environments []string
	if filter != nil {
		models = filter.GetModels()
		providers = filter.GetProviders()
		environments = filter.GetEnvironments()
	}

	metric := req.Msg.GetMetric()
	if metric == "" {
		metric = "requests"
	}
	groupBy := req.Msg.GetGroupBy()
	if groupBy == "" {
		groupBy = "model"
	}

	breakdownQuery := traceshandler.NewMetricsBreakdownQuery(
		startTime,
		endTime,
		metric,
		groupBy,
		int(req.Msg.GetLimit()),
		req.Msg.GetCompare(),
		models,
		providers,
		environments,
	)

	result, err := sys.QueryBus.Execute(ctx, breakdownQuery)
	if err != nil {
		logger.WithError(err).Error("failed to get metrics breakdown")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	response, ok := result.(*query.Response)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid response type"))
	}

	breakdown, ok := response.Data.(traceshandler.MetricsBreakdownResult)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid data type"))
	}

	rows := make([]*tracespb.BreakdownRow, 0, len(breakdown.Rows))
	for _, row := range breakdown.Rows {
		rows = append(rows, &tracespb.BreakdownRow{
			Key:           row.Key,
			Value:         row.Value,
			RequestCount:  int64(row.RequestCount),
			PreviousValue: row.PreviousValue,
			Provider:      row.Provider,
		})
	}

	return connect.NewResponse(&tracespb.GetMetricsBreakdownResponse{
		Rows:        rows,
		TotalGroups: int64(breakdown.TotalGroups),
	}), nil
}

func metricsTimeSeriesToProto(seriesData []traceshandler.MetricsTimeSeriesResult) []*tracespb.MetricsTimeSeries {
	pbSeries := make([]*tracespb.MetricsTimeSeries, 0, len(seriesData))
	for _, s := range seriesData {
		pbBuckets := make([]*tracespb.TimeSeriesBucket, 0, len(s.Buckets))
		for _, b := range s.Buckets {
			pbBuckets = append(pbBuckets, &tracespb.TimeSeriesBucket{
				Timestamp: timestamppb.New(b.Timestamp),
				Value:     b.Value,
				Label:     b.Label,
			})
		}
		pbSeries = append(pbSeries, &tracespb.MetricsTimeSeries{
			Buckets:    pbBuckets,
			MetricName: s.MetricName,
		})
	}
	return pbSeries
}

func metricsDashboardToProto(dashboard traceshandler.MetricsDashboardResult) *tracespb.MetricsDashboard {
	return &tracespb.MetricsDashboard{
		TotalRequests:     int64(dashboard.TotalRequests),
		AvgLatencyMs:      dashboard.AvgLatencyMs,
		TotalCost:         dashboard.TotalCost,
		ErrorRate:         dashboard.ErrorRate,
		TotalTokens:       int64(dashboard.TotalTokens),
		TotalInputTokens:  int64(dashboard.TotalInputTokens),
		TotalOutputTokens: int64(dashboard.TotalOutputTokens),
		UniqueModels:      int32(dashboard.UniqueModels),
		UniqueProviders:   int32(dashboard.UniqueProviders),
		TotalAgentTurns:   int64(dashboard.TotalAgentTurns),
		AvgAgentTurnMs:    dashboard.AvgAgentTurnMs,
		P50LatencyMs:      dashboard.P50LatencyMs,
		P95LatencyMs:      dashboard.P95LatencyMs,
		P99LatencyMs:      dashboard.P99LatencyMs,
		TotalErrors:       int64(dashboard.TotalErrors),
		TtftP50Ms:         dashboard.TtftP50Ms,
		TtftP95Ms:         dashboard.TtftP95Ms,
	}
}

func metricsDeltasToProto(current, previous traceshandler.MetricsDashboardResult) *tracespb.MetricsDeltas {
	return &tracespb.MetricsDeltas{
		Requests:   pct(float64(current.TotalRequests), float64(previous.TotalRequests)),
		Errors:     pct(float64(current.TotalErrors), float64(previous.TotalErrors)),
		ErrorRate:  pct(current.ErrorRate, previous.ErrorRate),
		Cost:       pct(current.TotalCost, previous.TotalCost),
		Tokens:     pct(float64(current.TotalTokens), float64(previous.TotalTokens)),
		AvgLatency: pct(current.AvgLatencyMs, previous.AvgLatencyMs),
		P95Latency: pct(current.P95LatencyMs, previous.P95LatencyMs),
		TtftP50:    pct(current.TtftP50Ms, previous.TtftP50Ms),
		TtftP95:    pct(current.TtftP95Ms, previous.TtftP95Ms),
	}
}

func pct(current, previous float64) float64 {
	if previous == 0 {
		return 0
	}
	return (current - previous) / previous
}

// ============================================================================
// ListTraceSessions
// ============================================================================

func (s *Server) ListTraceSessions(
	ctx context.Context,
	req *connect.Request[tracespb.ListTraceSessionsRequest],
) (*connect.Response[tracespb.ListTraceSessionsResponse], error) {
	sys, err := s.getSystem(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	filter := req.Msg.GetFilter()
	startTime, endTime := parseFilterTimes(filter)

	userID := ""
	if req.Msg.UserId != nil {
		userID = *req.Msg.UserId
	}
	search := ""
	if req.Msg.Search != nil {
		search = *req.Msg.Search
	}

	limit := int(req.Msg.GetLimit())
	if limit <= 0 {
		limit = 50
	}
	offset := int(req.Msg.GetOffset())

	listQuery := traceshandler.NewListSessionsQuery(
		startTime, endTime,
		userID, search,
		req.Msg.GetOrderBy(),
		limit, offset,
	)

	result, err := sys.QueryBus.Execute(ctx, listQuery)
	if err != nil {
		logger.WithError(err).Error("failed to list trace sessions")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	response, ok := result.(*query.Response)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid response type"))
	}

	listResult, ok := response.Data.(traceshandler.SessionListResult)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid data type"))
	}

	pbSessions := make([]*tracespb.TraceSession, 0, len(listResult.Sessions))
	for _, sess := range listResult.Sessions {
		pbSessions = append(pbSessions, sessionToProto(&sess))
	}

	return connect.NewResponse(&tracespb.ListTraceSessionsResponse{
		Sessions:   pbSessions,
		TotalCount: int32(listResult.TotalCount),
	}), nil
}

// ============================================================================
// GetTraceSession
// ============================================================================

func (s *Server) GetTraceSession(
	ctx context.Context,
	req *connect.Request[tracespb.GetTraceSessionRequest],
) (*connect.Response[tracespb.GetTraceSessionResponse], error) {
	sys, err := s.getSystem(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	sessionID := req.Msg.GetSessionId()
	if sessionID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("session_id is required"))
	}

	getQuery := traceshandler.NewGetSessionQuery(sessionID)

	result, err := sys.QueryBus.Execute(ctx, getQuery)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("session not found: %s", sessionID))
		}
		logger.WithError(err).Error("failed to get trace session")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	response, ok := result.(*query.Response)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid response type"))
	}

	sess, ok := response.Data.(traceshandler.SessionReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid data type"))
	}

	return connect.NewResponse(&tracespb.GetTraceSessionResponse{
		Session: sessionToProto(&sess),
	}), nil
}

// ============================================================================
// ListTraceUsers
// ============================================================================

func (s *Server) ListTraceUsers(
	ctx context.Context,
	req *connect.Request[tracespb.ListTraceUsersRequest],
) (*connect.Response[tracespb.ListTraceUsersResponse], error) {
	sys, err := s.getSystem(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	filter := req.Msg.GetFilter()
	startTime, endTime := parseFilterTimes(filter)

	search := ""
	if req.Msg.Search != nil {
		search = *req.Msg.Search
	}

	limit := int(req.Msg.GetLimit())
	if limit <= 0 {
		limit = 50
	}
	offset := int(req.Msg.GetOffset())

	listQuery := traceshandler.NewListUsersQuery(
		startTime, endTime,
		search,
		req.Msg.GetOrderBy(),
		limit, offset,
	)

	result, err := sys.QueryBus.Execute(ctx, listQuery)
	if err != nil {
		logger.WithError(err).Error("failed to list trace users")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	response, ok := result.(*query.Response)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid response type"))
	}

	listResult, ok := response.Data.(traceshandler.UserListResult)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid data type"))
	}

	pbUsers := make([]*tracespb.TraceUser, 0, len(listResult.Users))
	for _, u := range listResult.Users {
		pbUsers = append(pbUsers, userToProto(&u))
	}

	return connect.NewResponse(&tracespb.ListTraceUsersResponse{
		Users:      pbUsers,
		TotalCount: int32(listResult.TotalCount),
	}), nil
}

// ============================================================================
// GetTraceUser
// ============================================================================

func (s *Server) GetTraceUser(
	ctx context.Context,
	req *connect.Request[tracespb.GetTraceUserRequest],
) (*connect.Response[tracespb.GetTraceUserResponse], error) {
	sys, err := s.getSystem(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	userID := req.Msg.GetUserId()
	if userID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("user_id is required"))
	}

	getQuery := traceshandler.NewGetUserQuery(userID)

	result, err := sys.QueryBus.Execute(ctx, getQuery)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("user not found: %s", userID))
		}
		logger.WithError(err).Error("failed to get trace user")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	response, ok := result.(*query.Response)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid response type"))
	}

	u, ok := response.Data.(traceshandler.UserReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid data type"))
	}

	return connect.NewResponse(&tracespb.GetTraceUserResponse{
		User: userToProto(&u),
	}), nil
}

// ============================================================================
// GetOutcomeDashboard
// ============================================================================

func (s *Server) GetOutcomeDashboard(
	ctx context.Context,
	req *connect.Request[tracespb.GetOutcomeDashboardRequest],
) (*connect.Response[tracespb.GetOutcomeDashboardResponse], error) {
	sys, err := s.getSystem(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	filter := req.Msg.GetFilter()
	startTime, endTime := parseFilterTimes(filter)

	dashQuery := traceshandler.NewOutcomeDashboardQuery(
		startTime, endTime,
		req.Msg.GetAgentId(),
		req.Msg.GetGroupBy()...,
	)

	result, err := sys.QueryBus.Execute(ctx, dashQuery)
	if err != nil {
		logger.WithError(err).Error("failed to get outcome dashboard")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	response, ok := result.(*query.Response)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid response type"))
	}

	dashboard, ok := response.Data.(traceshandler.OutcomeDashboardResult)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid data type"))
	}

	pbScores := make([]*tracespb.OutcomeScoreSummary, 0, len(dashboard.Scores))
	for _, sc := range dashboard.Scores {
		pbScores = append(pbScores, &tracespb.OutcomeScoreSummary{
			ScoreName: sc.ScoreName,
			DataType:  sc.DataType,
			Count:     int64(sc.Count),
			Mean:      sc.Mean,
			Min:       sc.Min,
			Max:       sc.Max,
			P50:       sc.P50,
			P95:       sc.P95,
			PassRate:  sc.PassRate,
		})
	}

	pbBreakdowns := make([]*tracespb.VerdictBreakdown, 0, len(dashboard.VerdictBreakdowns))
	for _, bd := range dashboard.VerdictBreakdowns {
		entries := make([]*tracespb.VerdictBreakdownEntry, 0, len(bd.Entries))
		for _, e := range bd.Entries {
			entries = append(entries, &tracespb.VerdictBreakdownEntry{
				GroupKey: e.GroupKey,
				Rates:    toProtoVerdictRates(e.Rates),
			})
		}
		pbBreakdowns = append(pbBreakdowns, &tracespb.VerdictBreakdown{
			Dimension: bd.Dimension,
			Entries:   entries,
		})
	}

	return connect.NewResponse(&tracespb.GetOutcomeDashboardResponse{
		Dashboard: &tracespb.OutcomeDashboard{
			TaskCompletionRate:   dashboard.TaskCompletionRate,
			ToolSuccessRate:      dashboard.ToolSuccessRate,
			PolicyComplianceRate: dashboard.PolicyComplianceRate,
			LoopHealthRate:       dashboard.LoopHealthRate,
			IterationEfficiency:  dashboard.IterationEfficiency,
			SandboxSuccessRate:   dashboard.SandboxSuccessRate,
			TotalEvaluations:     int64(dashboard.TotalEvaluations),
			UniqueSessions:       int64(dashboard.UniqueSessions),
		},
		Scores:            pbScores,
		VerdictRates:      toProtoVerdictRates(dashboard.VerdictRates),
		VerdictBreakdowns: pbBreakdowns,
	}), nil
}

func toProtoVerdictRates(r traceshandler.VerdictRates) *tracespb.VerdictRates {
	return &tracespb.VerdictRates{
		WinRate:      r.WinRate,
		FailRate:     r.FailRate,
		DrawRate:     r.DrawRate,
		NoChangeRate: r.NoChangeRate,
		SampleSize:   int64(r.SampleSize),
	}
}

// ============================================================================
// GetOutcomeTimeSeries
// ============================================================================

func (s *Server) GetOutcomeTimeSeries(
	ctx context.Context,
	req *connect.Request[tracespb.GetOutcomeTimeSeriesRequest],
) (*connect.Response[tracespb.GetOutcomeTimeSeriesResponse], error) {
	sys, err := s.getSystem(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	filter := req.Msg.GetFilter()
	startTime, endTime := parseFilterTimes(filter)

	scoreName := req.Msg.GetScoreName()
	if scoreName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("score_name is required"))
	}

	aggregation := req.Msg.GetAggregation()
	if aggregation == "" {
		aggregation = "avg"
	}

	granularity := normalizeGranularity(req.Msg.GetGranularity())

	tsQuery := traceshandler.NewOutcomeTimeSeriesQuery(
		startTime, endTime,
		scoreName, aggregation, granularity,
		req.Msg.GetAgentId(),
		req.Msg.GetGroupBy(),
	)

	result, err := sys.QueryBus.Execute(ctx, tsQuery)
	if err != nil {
		logger.WithError(err).Error("failed to get outcome time series")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	response, ok := result.(*query.Response)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid response type"))
	}

	seriesData, ok := response.Data.([]traceshandler.MetricsTimeSeriesResult)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid data type"))
	}

	pbSeries := make([]*tracespb.MetricsTimeSeries, 0, len(seriesData))
	for _, s := range seriesData {
		pbBuckets := make([]*tracespb.TimeSeriesBucket, 0, len(s.Buckets))
		for _, b := range s.Buckets {
			pbBuckets = append(pbBuckets, &tracespb.TimeSeriesBucket{
				Timestamp: timestamppb.New(b.Timestamp),
				Value:     b.Value,
				Label:     b.Label,
			})
		}
		pbSeries = append(pbSeries, &tracespb.MetricsTimeSeries{
			Buckets:    pbBuckets,
			MetricName: s.MetricName,
		})
	}

	return connect.NewResponse(&tracespb.GetOutcomeTimeSeriesResponse{
		Series: pbSeries,
	}), nil
}

// ============================================================================
// Helpers
// ============================================================================

func (s *Server) getSystem(ctx context.Context) (*cqrs.System, error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	return sys, err
}

func normalizeGranularity(g string) string {
	switch g {
	case "1m", "minute":
		return "minute"
	case "5m", "10m", "15m", "30m", "5minute":
		return "5minute"
	case "6h", "12h", "6hour":
		return "6hour"
	case "1d", "7d", "30d", "day":
		return "day"
	case "1h", "hour", "":
		return "hour"
	default:
		return "hour"
	}
}

func parseFilterTimes(filter *tracespb.MetricsFilter) (time.Time, time.Time) {
	startTime := time.Now().Add(-24 * time.Hour)
	endTime := time.Now()

	if filter != nil {
		if filter.GetStartTime() != nil {
			startTime = filter.GetStartTime().AsTime()
		}
		if filter.GetEndTime() != nil {
			endTime = filter.GetEndTime().AsTime()
		}
	}

	return startTime, endTime
}

func sessionToProto(s *traceshandler.SessionReadModel) *tracespb.TraceSession {
	return &tracespb.TraceSession{
		SessionId:         s.SessionID,
		UserId:            s.UserID,
		FirstTraceAt:      timestamppb.New(s.FirstTraceAt),
		LastTraceAt:       timestamppb.New(s.LastTraceAt),
		TraceCount:        int32(s.TraceCount),
		TotalDurationNs:   int64(s.TotalDurationNs),
		TotalInputTokens:  int64(s.TotalInputTokens),
		TotalOutputTokens: int64(s.TotalOutputTokens),
		TotalCost:         s.TotalCost,
		ErrorCount:        int32(s.ErrorCount),
		Models:            s.Models,
		Tags:              s.Tags,
		Environment:       s.Environment,
		Kinds:             s.Kinds,
	}
}

func userToProto(u *traceshandler.UserReadModel) *tracespb.TraceUser {
	return &tracespb.TraceUser{
		UserId:       u.UserID,
		FirstSeen:    timestamppb.New(u.FirstSeen),
		LastSeen:     timestamppb.New(u.LastSeen),
		SessionCount: int32(u.SessionCount),
		TraceCount:   int32(u.TraceCount),
		TotalTokens:  int64(u.TotalTokens),
		TotalCost:    u.TotalCost,
		ErrorRate:    u.ErrorRate,
		AvgLatencyNs: int64(u.AvgLatencyNs),
	}
}
