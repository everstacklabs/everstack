package v1

import (
	"context"
	"encoding/json"
	"errors"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/cqrs"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/utils"
	"github.com/everstacklabs/everstack/internal/query"
	datasetsquery "github.com/everstacklabs/everstack/internal/query/handlers/datasets"
	eval_runner "github.com/everstacklabs/everstack/internal/services/eval_runner"
	datasetspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/datasets/v1"
	"github.com/jmoiron/sqlx"
	"google.golang.org/protobuf/types/known/structpb"
)

// SetBaseline marks an eval run as the baseline for regression detection.
func (s *EvalServer) SetBaseline(ctx context.Context, req *connect.Request[datasetspb.SetBaselineRequest]) (*connect.Response[datasetspb.SetBaselineResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID, err := requireTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	ctx = ensureTenantSchema(ctx, tenantID)

	db, err := getDBFromContext(ctx, sys)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if err := eval_runner.SetBaseline(ctx, db, tenantID, req.Msg.GetEvalRunId()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Fetch the updated run
	q := datasetsquery.NewGetEvalRunQuery(req.Msg.GetEvalRunId(), tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}
	rm, ok := data.(*datasetsquery.EvalRunReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("unexpected data type"))
	}

	return connect.NewResponse(&datasetspb.SetBaselineResponse{
		EvalRun: evalRunToProto(rm),
	}), nil
}

// --- Schedule CRUD ---

func (s *EvalServer) CreateEvalSchedule(ctx context.Context, req *connect.Request[datasetspb.CreateEvalScheduleRequest]) (*connect.Response[datasetspb.CreateEvalScheduleResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID, err := requireTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	ctx = ensureTenantSchema(ctx, tenantID)

	db, err := getDBFromContext(ctx, sys)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	evalConfigMap := map[string]interface{}{}
	if req.Msg.EvalConfig != nil {
		evalConfigMap = req.Msg.EvalConfig.AsMap()
	}
	if req.Msg.GetDatasetVersionId() != "" {
		evalConfigMap["dataset_version_id"] = req.Msg.GetDatasetVersionId()
	}
	var evalConfig []byte
	if len(evalConfigMap) > 0 {
		evalConfig, _ = json.Marshal(evalConfigMap)
	}

	rec := &eval_runner.EvalScheduleRecord{
		TenantID:        tenantID,
		Name:            req.Msg.GetName(),
		Description:     req.Msg.GetDescription(),
		DatasetID:       req.Msg.GetDatasetId(),
		EvalTargetType:  req.Msg.GetEvalTargetType(),
		EvalTargetID:    req.Msg.GetEvalTargetId(),
		EvalConfig:      evalConfig,
		ScorerConfigIDs: req.Msg.GetScorerConfigIds(),
		CronExpression:  req.Msg.GetCronExpression(),
		Timezone:        req.Msg.GetTimezone(),
	}

	if err := eval_runner.CreateSchedule(ctx, db, rec); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Fetch back
	sched, err := eval_runner.GetSchedule(ctx, db, rec.ID, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&datasetspb.CreateEvalScheduleResponse{
		Schedule: scheduleToProto(sched),
	}), nil
}

func (s *EvalServer) GetEvalSchedule(ctx context.Context, req *connect.Request[datasetspb.GetEvalScheduleRequest]) (*connect.Response[datasetspb.GetEvalScheduleResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	ctx = ensureTenantSchema(ctx, tenantID)

	db, err := getDBFromContext(ctx, sys)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	sched, err := eval_runner.GetSchedule(ctx, db, req.Msg.GetId(), tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	return connect.NewResponse(&datasetspb.GetEvalScheduleResponse{
		Schedule: scheduleToProto(sched),
	}), nil
}

func (s *EvalServer) ListEvalSchedules(ctx context.Context, req *connect.Request[datasetspb.ListEvalSchedulesRequest]) (*connect.Response[datasetspb.ListEvalSchedulesResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	ctx = ensureTenantSchema(ctx, tenantID)

	db, err := getDBFromContext(ctx, sys)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	limit := int(req.Msg.GetLimit())
	offset := int(req.Msg.GetOffset())
	if limit == 0 {
		limit = 50
	}

	records, total, err := eval_runner.ListSchedules(ctx, db, tenantID, req.Msg.GetDatasetId(), limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var schedules []*datasetspb.EvalSchedule
	for i := range records {
		schedules = append(schedules, scheduleToProto(&records[i]))
	}

	return connect.NewResponse(&datasetspb.ListEvalSchedulesResponse{
		Schedules: schedules,
		Total:     int32(total),
	}), nil
}

func (s *EvalServer) UpdateEvalSchedule(ctx context.Context, req *connect.Request[datasetspb.UpdateEvalScheduleRequest]) (*connect.Response[datasetspb.UpdateEvalScheduleResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	ctx = ensureTenantSchema(ctx, tenantID)

	db, err := getDBFromContext(ctx, sys)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	updates := make(map[string]interface{})
	if req.Msg.Name != nil {
		updates["name"] = *req.Msg.Name
	}
	if req.Msg.Description != nil {
		updates["description"] = *req.Msg.Description
	}
	if req.Msg.CronExpression != nil {
		updates["cron_expression"] = *req.Msg.CronExpression
	}
	if req.Msg.Timezone != nil {
		updates["timezone"] = *req.Msg.Timezone
	}
	if req.Msg.Enabled != nil {
		updates["enabled"] = *req.Msg.Enabled
	}
	if len(req.Msg.ScorerConfigIds) > 0 {
		updates["scorer_config_ids"] = req.Msg.ScorerConfigIds
	}

	if err := eval_runner.UpdateSchedule(ctx, db, req.Msg.GetId(), tenantID, updates); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	sched, err := eval_runner.GetSchedule(ctx, db, req.Msg.GetId(), tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&datasetspb.UpdateEvalScheduleResponse{
		Schedule: scheduleToProto(sched),
	}), nil
}

func (s *EvalServer) DeleteEvalSchedule(ctx context.Context, req *connect.Request[datasetspb.DeleteEvalScheduleRequest]) (*connect.Response[datasetspb.DeleteEvalScheduleResponse], error) {
	sys, err := s.getSys(ctx)
	if err != nil {
		return nil, err
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	ctx = ensureTenantSchema(ctx, tenantID)

	db, err := getDBFromContext(ctx, sys)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if err := eval_runner.DeleteSchedule(ctx, db, req.Msg.GetId(), tenantID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&datasetspb.DeleteEvalScheduleResponse{
		Success: true,
		Message: "Schedule deleted",
	}), nil
}

// --- Helpers ---

func getDBFromContext(ctx context.Context, sys *cqrs.System) (*sqlx.DB, error) {
	if db, ok := ctx.Value(contextkeys.PrimaryDB).(*sqlx.DB); ok && db != nil {
		return db, nil
	}
	if sys.ProjectionManager != nil {
		if db := sys.ProjectionManager.DB(); db != nil {
			return db, nil
		}
	}
	return nil, errors.New("primary db not available")
}

func scheduleToProto(rec *eval_runner.EvalScheduleRecord) *datasetspb.EvalSchedule {
	sched := &datasetspb.EvalSchedule{
		Id:              rec.ID,
		TenantId:        rec.TenantID,
		Name:            rec.Name,
		Description:     rec.Description,
		DatasetId:       rec.DatasetID,
		EvalTargetType:  rec.EvalTargetType,
		EvalTargetId:    rec.EvalTargetID,
		ScorerConfigIds: rec.ScorerConfigIDs,
		CronExpression:  rec.CronExpression,
		Timezone:        rec.Timezone,
		Enabled:         rec.Enabled,
		CreatedAt:       utils.ParseTimestamp(rec.CreatedAt),
		UpdatedAt:       utils.ParseTimestamp(rec.UpdatedAt),
	}

	if len(rec.EvalConfig) > 0 {
		var m map[string]interface{}
		if err := json.Unmarshal(rec.EvalConfig, &m); err == nil {
			if s, err := structpb.NewStruct(m); err == nil {
				sched.EvalConfig = s
			}
		}
	}

	if rec.LastRunAt.Valid {
		sched.LastRunAt = utils.ParseTimestamp(rec.LastRunAt.String)
	}
	if rec.NextRunAt.Valid {
		sched.NextRunAt = utils.ParseTimestamp(rec.NextRunAt.String)
	}

	return sched
}
