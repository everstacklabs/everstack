package v1

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"connectrpc.com/connect"
	annotationscmd "github.com/everstacklabs/everstack/internal/commands/handlers/annotations"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/enterprise"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/utils"
	"github.com/everstacklabs/everstack/internal/query"
	annotationsquery "github.com/everstacklabs/everstack/internal/query/handlers/annotations"
	traceshandler "github.com/everstacklabs/everstack/internal/query/handlers/traces"
	annotationspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/annotations/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// getSys retrieves the CQRS system from the request context or the server's stored context.
func (s *Server) getSys(ctx context.Context) (*cqrs.System, error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}
	return sys, nil
}

// getTenantID returns the tenant id set by the auth middleware. The
// reqTenantID argument is intentionally ignored — accepting a body-supplied
// tenant id was the leak shape the cross-tenant fixes elsewhere already
// closed (datasets, memory, agents). Empty result means "request is
// unauthenticated"; callers must reject with a clean error rather than
// query unscoped.
func getTenantID(ctx context.Context, _ string) string {
	return contextkeys.GetTenantID(ctx)
}

func annotationCommandError(err error) error {
	if errors.Is(err, annotationscmd.ErrAnnotationItemPermissionDenied) {
		return connect.NewError(connect.CodePermissionDenied, errors.New("annotation item access denied"))
	}
	return connect.NewError(connect.CodeInternal, err)
}

// ---------------------------------------------------------------------------
// Queue CRUD
// ---------------------------------------------------------------------------

func (s *Server) CreateQueue(ctx context.Context, req *connect.Request[annotationspb.CreateQueueRequest]) (*connect.Response[annotationspb.CreateQueueResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	userID := contextkeys.GetUserID(ctx)

	if err := enterprise.CheckResourceLimit(ctx, s.db, enterprise.LicenseMonitorFromContext(ctx),
		enterprise.UsageTypeAnnotationQueues,
		`SELECT COUNT(*) FROM annotation_queues WHERE tenant_id = $1 AND deleted_at IS NULL`,
		[]interface{}{tenantID}, 1, "annotation queue"); err != nil {
		return nil, connect.NewError(connect.CodeResourceExhausted, err)
	}

	cmd := annotationscmd.NewCreateQueueCommand(
		tenantID,
		req.Msg.GetName(),
		req.Msg.GetDescription(),
		queueStatusToString(req.Msg.GetStatus()),
		assignmentModeToString(req.Msg.GetAssignmentMode()),
		req.Msg.GetScoreConfigIds(),
		req.Msg.GetAnnotators(),
		req.Msg.GetAutoPopulateConfig(),
		userID,
		"",
	)

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &annotationspb.CreateQueueResponse{
		Queue: &annotationspb.AnnotationQueue{
			Id:             cmd.ID,
			TenantId:       tenantID,
			Name:           req.Msg.GetName(),
			Status:         req.Msg.GetStatus(),
			AssignmentMode: req.Msg.GetAssignmentMode(),
		},
	}
	return connect.NewResponse(resp), nil
}

func (s *Server) GetQueue(ctx context.Context, req *connect.Request[annotationspb.GetQueueRequest]) (*connect.Response[annotationspb.GetQueueResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	userID := contextkeys.GetUserID(ctx)

	if err := s.assertQueueAnnotator(ctx, req.Msg.GetId(), tenantID, userID); err != nil {
		return nil, err
	}

	q := annotationsquery.NewGetQueueByIDQuery(req.Msg.GetId(), tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if res == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("annotation queue not found"))
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}

	rm, ok := data.(*annotationsquery.QueueReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("unexpected data type"))
	}

	return connect.NewResponse(&annotationspb.GetQueueResponse{Queue: queueReadModelToProto(rm)}), nil
}

func (s *Server) ListQueues(ctx context.Context, req *connect.Request[annotationspb.ListQueuesRequest]) (*connect.Response[annotationspb.ListQueuesResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())

	var status *string
	if req.Msg.GetStatus() != annotationspb.QueueStatus_QUEUE_STATUS_UNSPECIFIED {
		s := queueStatusToString(req.Msg.GetStatus())
		status = &s
	}

	q := annotationsquery.NewListQueuesQuery(tenantID, status, int(req.Msg.GetLimit()), int(req.Msg.GetOffset()))
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var queues []*annotationspb.AnnotationQueue
	if res != nil {
		var data interface{} = res
		if resp, ok := res.(*query.Response); ok {
			data = resp.Data
		}
		if list, ok := data.([]annotationsquery.QueueReadModel); ok {
			for i := range list {
				queues = append(queues, queueReadModelToProto(&list[i]))
			}
		}
	}

	return connect.NewResponse(&annotationspb.ListQueuesResponse{Queues: queues}), nil
}

func (s *Server) UpdateQueue(ctx context.Context, req *connect.Request[annotationspb.UpdateQueueRequest]) (*connect.Response[annotationspb.UpdateQueueResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	userID := contextkeys.GetUserID(ctx)

	cmd := annotationscmd.NewUpdateQueueCommand(req.Msg.GetId(), tenantID, userID, "")

	if req.Msg.Name != nil {
		cmd.Name = req.Msg.Name
	}
	if req.Msg.Description != nil {
		cmd.Description = req.Msg.Description
	}
	if req.Msg.GetStatus() != annotationspb.QueueStatus_QUEUE_STATUS_UNSPECIFIED {
		st := queueStatusToString(req.Msg.GetStatus())
		cmd.Status = &st
	}
	if len(req.Msg.GetScoreConfigIds()) > 0 {
		cmd.ScoreConfigIDs = req.Msg.GetScoreConfigIds()
	}
	if req.Msg.GetAssignmentMode() != annotationspb.AssignmentMode_ASSIGNMENT_MODE_UNSPECIFIED {
		am := assignmentModeToString(req.Msg.GetAssignmentMode())
		cmd.AssignmentMode = &am
	}
	if len(req.Msg.GetAnnotators()) > 0 {
		cmd.Annotators = req.Msg.GetAnnotators()
	}
	if req.Msg.AutoPopulateConfig != nil {
		cmd.AutoPopulateConfig = req.Msg.AutoPopulateConfig
	}

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&annotationspb.UpdateQueueResponse{
		Queue: &annotationspb.AnnotationQueue{
			Id:       req.Msg.GetId(),
			TenantId: tenantID,
		},
	}), nil
}

func (s *Server) DeleteQueue(ctx context.Context, req *connect.Request[annotationspb.DeleteQueueRequest]) (*connect.Response[annotationspb.DeleteQueueResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	userID := contextkeys.GetUserID(ctx)

	cmd := annotationscmd.NewDeleteQueueCommand(req.Msg.GetId(), tenantID, userID, "")

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&annotationspb.DeleteQueueResponse{
		Success: true,
		Message: "annotation queue deletion dispatched",
	}), nil
}

// ---------------------------------------------------------------------------
// Queue Items
// ---------------------------------------------------------------------------

func (s *Server) AddItemToQueue(ctx context.Context, req *connect.Request[annotationspb.AddItemToQueueRequest]) (*connect.Response[annotationspb.AddItemToQueueResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	userID := contextkeys.GetUserID(ctx)

	cmd := annotationscmd.NewAddItemToQueueCommand(
		tenantID,
		req.Msg.GetQueueId(),
		req.Msg.GetTraceId(),
		req.Msg.GetObservationId(),
		req.Msg.GetPriority(),
		userID,
		"",
	)

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&annotationspb.AddItemToQueueResponse{
		Item: &annotationspb.AnnotationQueueItem{
			Id:      cmd.ID,
			QueueId: req.Msg.GetQueueId(),
			TraceId: req.Msg.GetTraceId(),
			Status:  annotationspb.QueueItemStatus_QUEUE_ITEM_STATUS_PENDING,
		},
	}), nil
}

func (s *Server) AddItemsToQueueBatch(ctx context.Context, req *connect.Request[annotationspb.AddItemsToQueueBatchRequest]) (*connect.Response[annotationspb.AddItemsToQueueBatchResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	userID := contextkeys.GetUserID(ctx)

	items := make([]annotationscmd.BatchItem, 0, len(req.Msg.GetItems()))
	for _, entry := range req.Msg.GetItems() {
		items = append(items, annotationscmd.BatchItem{
			TraceID:       entry.GetTraceId(),
			ObservationID: entry.GetObservationId(),
			Priority:      entry.GetPriority(),
		})
	}

	cmd := annotationscmd.NewAddItemsToQueueBatchCommand(tenantID, req.Msg.GetQueueId(), items, userID, "")

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&annotationspb.AddItemsToQueueBatchResponse{
		AddedCount: int32(len(items)),
	}), nil
}

func (s *Server) GetNextItem(ctx context.Context, req *connect.Request[annotationspb.GetNextItemRequest]) (*connect.Response[annotationspb.GetNextItemResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	userID := contextkeys.GetUserID(ctx)

	if err := s.assertQueueAnnotator(ctx, req.Msg.GetQueueId(), tenantID, userID); err != nil {
		return nil, err
	}

	q := annotationsquery.NewGetNextItemQuery(tenantID, req.Msg.GetQueueId(), userID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if res == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no pending items in queue"))
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}

	rm, ok := data.(*annotationsquery.QueueItemReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("unexpected data type"))
	}

	return connect.NewResponse(&annotationspb.GetNextItemResponse{Item: queueItemReadModelToProto(rm)}), nil
}

func (s *Server) SubmitAnnotation(ctx context.Context, req *connect.Request[annotationspb.SubmitAnnotationRequest]) (*connect.Response[annotationspb.SubmitAnnotationResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	userID := contextkeys.GetUserID(ctx)

	if err := s.assertItemQueueAnnotator(ctx, req.Msg.GetItemId(), tenantID, userID); err != nil {
		return nil, err
	}

	scores := make([]annotationscmd.ScoreEntry, 0, len(req.Msg.GetScores()))
	for _, s := range req.Msg.GetScores() {
		scores = append(scores, annotationscmd.ScoreEntry{
			ScoreConfigID: s.GetScoreConfigId(),
			ScoreID:       s.GetScoreId(),
		})
	}

	completedBy := req.Msg.GetCompletedBy()
	if completedBy == "" {
		completedBy = userID
	}

	cmd := annotationscmd.NewSubmitAnnotationCommand(tenantID, req.Msg.GetItemId(), completedBy, scores, userID, "")

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, annotationCommandError(err)
	}

	return connect.NewResponse(&annotationspb.SubmitAnnotationResponse{
		Item: &annotationspb.AnnotationQueueItem{
			Id:          req.Msg.GetItemId(),
			CompletedBy: completedBy,
			Status:      annotationspb.QueueItemStatus_QUEUE_ITEM_STATUS_COMPLETED,
		},
	}), nil
}

func (s *Server) SkipItem(ctx context.Context, req *connect.Request[annotationspb.SkipItemRequest]) (*connect.Response[annotationspb.SkipItemResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	userID := contextkeys.GetUserID(ctx)

	if err := s.assertItemQueueAnnotator(ctx, req.Msg.GetItemId(), tenantID, userID); err != nil {
		return nil, err
	}

	skippedBy := req.Msg.GetSkippedBy()
	if skippedBy == "" {
		skippedBy = userID
	}

	cmd := annotationscmd.NewSkipItemCommand(tenantID, req.Msg.GetItemId(), skippedBy, userID, "")

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, annotationCommandError(err)
	}

	return connect.NewResponse(&annotationspb.SkipItemResponse{
		Item: &annotationspb.AnnotationQueueItem{
			Id:     req.Msg.GetItemId(),
			Status: annotationspb.QueueItemStatus_QUEUE_ITEM_STATUS_SKIPPED,
		},
	}), nil
}

func (s *Server) ListQueueItems(ctx context.Context, req *connect.Request[annotationspb.ListQueueItemsRequest]) (*connect.Response[annotationspb.ListQueueItemsResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	userID := contextkeys.GetUserID(ctx)

	if err := s.assertQueueAnnotator(ctx, req.Msg.GetQueueId(), tenantID, userID); err != nil {
		return nil, err
	}

	var status *string
	if req.Msg.GetStatus() != annotationspb.QueueItemStatus_QUEUE_ITEM_STATUS_UNSPECIFIED {
		st := queueItemStatusToString(req.Msg.GetStatus())
		status = &st
	}

	var assignedTo *string
	if req.Msg.AssignedTo != nil {
		assignedTo = req.Msg.AssignedTo
	}

	q := annotationsquery.NewListQueueItemsQuery(tenantID, req.Msg.GetQueueId(), status, assignedTo, int(req.Msg.GetLimit()), int(req.Msg.GetOffset()))
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var items []*annotationspb.AnnotationQueueItem
	if res != nil {
		var data interface{} = res
		if resp, ok := res.(*query.Response); ok {
			data = resp.Data
		}
		if list, ok := data.([]annotationsquery.QueueItemReadModel); ok {
			for i := range list {
				items = append(items, queueItemReadModelToProto(&list[i]))
			}
		}
	}

	return connect.NewResponse(&annotationspb.ListQueueItemsResponse{Items: items}), nil
}

func (s *Server) GetQueueStats(ctx context.Context, req *connect.Request[annotationspb.GetQueueStatsRequest]) (*connect.Response[annotationspb.GetQueueStatsResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())

	q := annotationsquery.NewGetQueueStatsQuery(tenantID, req.Msg.GetQueueId())
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if res == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("queue not found"))
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}

	rm, ok := data.(*annotationsquery.QueueStatsReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("unexpected data type"))
	}

	return connect.NewResponse(&annotationspb.GetQueueStatsResponse{
		Stats: &annotationspb.QueueStats{
			QueueId:         rm.QueueID,
			TotalItems:      rm.TotalItems,
			PendingItems:    rm.PendingItems,
			InProgressItems: rm.InProgressItems,
			CompletedItems:  rm.CompletedItems,
			SkippedItems:    rm.SkippedItems,
		},
	}), nil
}

func (s *Server) PopulateFromTraces(ctx context.Context, req *connect.Request[annotationspb.PopulateFromTracesRequest]) (*connect.Response[annotationspb.PopulateFromTracesResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	userID := contextkeys.GetUserID(ctx)

	// Parse optional trace filter JSON
	var filter traceFilterCriteria
	if req.Msg.TraceFilter != nil && *req.Msg.TraceFilter != "" {
		if err := json.Unmarshal([]byte(*req.Msg.TraceFilter), &filter); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid trace_filter JSON"))
		}
	}

	// Determine max items (default 100, cap at 1000)
	maxItems := int32(100)
	if req.Msg.MaxItems != nil && *req.Msg.MaxItems > 0 {
		maxItems = *req.Msg.MaxItems
		if maxItems > 1000 {
			maxItems = 1000
		}
	}

	// Parse time filters
	var startTime, endTime time.Time
	if filter.StartTime != "" {
		if t, err := time.Parse(time.RFC3339, filter.StartTime); err == nil {
			startTime = t
		}
	}
	if filter.EndTime != "" {
		if t, err := time.Parse(time.RFC3339, filter.EndTime); err == nil {
			endTime = t
		}
	}

	// Build ListTracesQuery
	listQuery := traceshandler.NewListTracesQuery(
		tenantID,
		startTime,
		endTime,
		filter.Model,
		filter.Provider,
		filter.StatusCode,
		"", // correlationID
		"", // userID (query user, not filter)
		"", // traceID
	)
	listQuery.Limit = int(maxItems)
	listQuery.FilterUserID = filter.UserID
	listQuery.FilterSessionID = filter.SessionID
	listQuery.Environment = filter.Environment
	listQuery.Tags = filter.Tags

	// Execute trace query
	res, err := sys.QueryBus.Execute(ctx, listQuery)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Unwrap result
	var traces []query.TraceReadModel
	if res != nil {
		var data interface{} = res
		if resp, ok := res.(*query.Response); ok {
			data = resp.Data
		}
		if list, ok := data.([]query.TraceReadModel); ok {
			traces = list
		}
	}

	if len(traces) == 0 {
		return connect.NewResponse(&annotationspb.PopulateFromTracesResponse{
			AddedCount: 0,
		}), nil
	}

	// Build batch items from traces
	items := make([]annotationscmd.BatchItem, 0, len(traces))
	for _, t := range traces {
		items = append(items, annotationscmd.BatchItem{
			TraceID: t.TraceID,
		})
	}

	// Dispatch batch add command
	cmd := annotationscmd.NewAddItemsToQueueBatchCommand(tenantID, req.Msg.GetQueueId(), items, userID, "")
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&annotationspb.PopulateFromTracesResponse{
		AddedCount: int32(len(items)),
	}), nil
}

type traceFilterCriteria struct {
	Model       string   `json:"model,omitempty"`
	Provider    string   `json:"provider,omitempty"`
	StatusCode  string   `json:"status_code,omitempty"`
	Environment string   `json:"environment,omitempty"`
	UserID      string   `json:"user_id,omitempty"`
	SessionID   string   `json:"session_id,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	StartTime   string   `json:"start_time,omitempty"`
	EndTime     string   `json:"end_time,omitempty"`
}

// ---------------------------------------------------------------------------
// Conversion helpers
// ---------------------------------------------------------------------------

func queueStatusToString(s annotationspb.QueueStatus) string {
	switch s {
	case annotationspb.QueueStatus_QUEUE_STATUS_ACTIVE:
		return "active"
	case annotationspb.QueueStatus_QUEUE_STATUS_PAUSED:
		return "paused"
	case annotationspb.QueueStatus_QUEUE_STATUS_ARCHIVED:
		return "archived"
	default:
		return "active"
	}
}

func stringToQueueStatus(s string) annotationspb.QueueStatus {
	switch s {
	case "active":
		return annotationspb.QueueStatus_QUEUE_STATUS_ACTIVE
	case "paused":
		return annotationspb.QueueStatus_QUEUE_STATUS_PAUSED
	case "archived":
		return annotationspb.QueueStatus_QUEUE_STATUS_ARCHIVED
	default:
		return annotationspb.QueueStatus_QUEUE_STATUS_UNSPECIFIED
	}
}

func assignmentModeToString(m annotationspb.AssignmentMode) string {
	switch m {
	case annotationspb.AssignmentMode_ASSIGNMENT_MODE_MANUAL:
		return "manual"
	case annotationspb.AssignmentMode_ASSIGNMENT_MODE_ROUND_ROBIN:
		return "round_robin"
	case annotationspb.AssignmentMode_ASSIGNMENT_MODE_RANDOM:
		return "random"
	default:
		return "manual"
	}
}

func stringToAssignmentMode(s string) annotationspb.AssignmentMode {
	switch s {
	case "manual":
		return annotationspb.AssignmentMode_ASSIGNMENT_MODE_MANUAL
	case "round_robin":
		return annotationspb.AssignmentMode_ASSIGNMENT_MODE_ROUND_ROBIN
	case "random":
		return annotationspb.AssignmentMode_ASSIGNMENT_MODE_RANDOM
	default:
		return annotationspb.AssignmentMode_ASSIGNMENT_MODE_UNSPECIFIED
	}
}

func queueItemStatusToString(s annotationspb.QueueItemStatus) string {
	switch s {
	case annotationspb.QueueItemStatus_QUEUE_ITEM_STATUS_PENDING:
		return "pending"
	case annotationspb.QueueItemStatus_QUEUE_ITEM_STATUS_IN_PROGRESS:
		return "in_progress"
	case annotationspb.QueueItemStatus_QUEUE_ITEM_STATUS_COMPLETED:
		return "completed"
	case annotationspb.QueueItemStatus_QUEUE_ITEM_STATUS_SKIPPED:
		return "skipped"
	default:
		return ""
	}
}

func stringToQueueItemStatus(s string) annotationspb.QueueItemStatus {
	switch s {
	case "pending":
		return annotationspb.QueueItemStatus_QUEUE_ITEM_STATUS_PENDING
	case "in_progress":
		return annotationspb.QueueItemStatus_QUEUE_ITEM_STATUS_IN_PROGRESS
	case "completed":
		return annotationspb.QueueItemStatus_QUEUE_ITEM_STATUS_COMPLETED
	case "skipped":
		return annotationspb.QueueItemStatus_QUEUE_ITEM_STATUS_SKIPPED
	default:
		return annotationspb.QueueItemStatus_QUEUE_ITEM_STATUS_UNSPECIFIED
	}
}

func queueReadModelToProto(rm *annotationsquery.QueueReadModel) *annotationspb.AnnotationQueue {
	q := &annotationspb.AnnotationQueue{
		Id:             rm.ID,
		TenantId:       rm.TenantID,
		Name:           rm.Name,
		Status:         stringToQueueStatus(rm.Status),
		ScoreConfigIds: rm.ScoreConfigIDs,
		AssignmentMode: stringToAssignmentMode(rm.AssignmentMode),
		Annotators:     rm.Annotators,
		ItemsPending:   rm.ItemsPending,
		ItemsCompleted: rm.ItemsCompleted,
		CreatedAt:      utils.ParseTimestamp(rm.CreatedAt),
		UpdatedAt:      utils.ParseTimestamp(rm.UpdatedAt),
	}

	if rm.Description.Valid {
		q.Description = rm.Description.String
	}

	if len(rm.AutoPopulateConfig) > 0 {
		q.AutoPopulateConfig = string(rm.AutoPopulateConfig)
	}

	return q
}

func queueItemReadModelToProto(rm *annotationsquery.QueueItemReadModel) *annotationspb.AnnotationQueueItem {
	item := &annotationspb.AnnotationQueueItem{
		Id:        rm.ID,
		QueueId:   rm.QueueID,
		TenantId:  rm.TenantID,
		TraceId:   rm.TraceID,
		Status:    stringToQueueItemStatus(rm.Status),
		Priority:  rm.Priority,
		CreatedAt: utils.ParseTimestamp(rm.CreatedAt),
		UpdatedAt: utils.ParseTimestamp(rm.UpdatedAt),
	}

	if rm.ObservationID.Valid {
		item.ObservationId = rm.ObservationID.String
	}
	if rm.AssignedTo.Valid {
		item.AssignedTo = rm.AssignedTo.String
	}
	if rm.AssignedAt.Valid {
		item.AssignedAt = timestamppb.New(rm.AssignedAt.Time)
	}
	if rm.CompletedBy.Valid {
		item.CompletedBy = rm.CompletedBy.String
	}
	if rm.CompletedAt.Valid {
		item.CompletedAt = timestamppb.New(rm.CompletedAt.Time)
	}

	return item
}
