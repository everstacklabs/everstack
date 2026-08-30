package v1

import (
	"context"
	"encoding/json"
	"fmt"

	"connectrpc.com/connect"
	eval_runner "github.com/everstacklabs/everstack/internal/services/eval_runner"
	datasetspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/datasets/v1"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// --- Sampling Eval Rules CRUD ---

func (s *EvalServer) CreateSamplingEvalRule(ctx context.Context, req *connect.Request[datasetspb.CreateSamplingEvalRuleRequest]) (*connect.Response[datasetspb.CreateSamplingEvalRuleResponse], error) {
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

	var filterBytes []byte
	if req.Msg.FilterPredicate != nil {
		filterBytes, _ = json.Marshal(req.Msg.FilterPredicate.AsMap())
	}

	rec := &eval_runner.SamplingEvalRuleRecord{
		TenantID:        tenantID,
		Name:            req.Msg.GetName(),
		Description:     req.Msg.GetDescription(),
		FilterPredicate: filterBytes,
		SampleRate:      req.Msg.GetSampleRate(),
		ScorerConfigIDs: req.Msg.GetScorerConfigIds(),
		LookbackSeconds: int(req.Msg.GetLookbackSeconds()),
		IntervalSeconds: int(req.Msg.GetIntervalSeconds()),
		Enabled:         req.Msg.GetEnabled(),
	}
	// Default to enabled if the field wasn't set (proto bool defaults to false).
	if req.Msg.Enabled == nil {
		rec.Enabled = true
	}

	if err := eval_runner.CreateSamplingEvalRule(ctx, db, rec); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	got, err := eval_runner.GetSamplingEvalRule(ctx, db, rec.ID, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&datasetspb.CreateSamplingEvalRuleResponse{
		Rule: samplingRuleToProto(got),
	}), nil
}

func (s *EvalServer) GetSamplingEvalRule(ctx context.Context, req *connect.Request[datasetspb.GetSamplingEvalRuleRequest]) (*connect.Response[datasetspb.GetSamplingEvalRuleResponse], error) {
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
	got, err := eval_runner.GetSamplingEvalRule(ctx, db, req.Msg.GetId(), tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&datasetspb.GetSamplingEvalRuleResponse{
		Rule: samplingRuleToProto(got),
	}), nil
}

func (s *EvalServer) ListSamplingEvalRules(ctx context.Context, req *connect.Request[datasetspb.ListSamplingEvalRulesRequest]) (*connect.Response[datasetspb.ListSamplingEvalRulesResponse], error) {
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
	enabledOnly := req.Msg.GetEnabledOnly()
	limit := int(req.Msg.GetLimit())
	offset := int(req.Msg.GetOffset())
	rules, total, err := eval_runner.ListSamplingEvalRules(ctx, db, tenantID, enabledOnly, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*datasetspb.SamplingEvalRule, 0, len(rules))
	for i := range rules {
		out = append(out, samplingRuleToProto(&rules[i]))
	}
	return connect.NewResponse(&datasetspb.ListSamplingEvalRulesResponse{
		Rules: out,
		Total: int32(total),
	}), nil
}

func (s *EvalServer) UpdateSamplingEvalRule(ctx context.Context, req *connect.Request[datasetspb.UpdateSamplingEvalRuleRequest]) (*connect.Response[datasetspb.UpdateSamplingEvalRuleResponse], error) {
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

	updates := map[string]interface{}{}
	if req.Msg.Name != nil {
		updates["name"] = *req.Msg.Name
	}
	if req.Msg.Description != nil {
		updates["description"] = *req.Msg.Description
	}
	if req.Msg.FilterPredicate != nil {
		b, _ := json.Marshal(req.Msg.FilterPredicate.AsMap())
		updates["filter_predicate"] = b
	}
	if req.Msg.SampleRate != nil {
		updates["sample_rate"] = *req.Msg.SampleRate
	}
	if len(req.Msg.ScorerConfigIds) > 0 {
		// Note: empty list intentionally not handled here — use a sentinel
		// if we ever need "clear all scorers". For now non-empty replaces.
		updates["scorer_config_ids"] = req.Msg.ScorerConfigIds
	}
	if req.Msg.LookbackSeconds != nil {
		updates["lookback_seconds"] = *req.Msg.LookbackSeconds
	}
	if req.Msg.IntervalSeconds != nil {
		updates["interval_seconds"] = *req.Msg.IntervalSeconds
	}
	if req.Msg.Enabled != nil {
		updates["enabled"] = *req.Msg.Enabled
	}

	if err := eval_runner.UpdateSamplingEvalRule(ctx, db, req.Msg.GetId(), tenantID, updates); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	got, err := eval_runner.GetSamplingEvalRule(ctx, db, req.Msg.GetId(), tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&datasetspb.UpdateSamplingEvalRuleResponse{
		Rule: samplingRuleToProto(got),
	}), nil
}

func (s *EvalServer) DeleteSamplingEvalRule(ctx context.Context, req *connect.Request[datasetspb.DeleteSamplingEvalRuleRequest]) (*connect.Response[datasetspb.DeleteSamplingEvalRuleResponse], error) {
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
	if err := eval_runner.DeleteSamplingEvalRule(ctx, db, req.Msg.GetId(), tenantID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&datasetspb.DeleteSamplingEvalRuleResponse{Success: true}), nil
}

// RunSamplingEvalRuleNow is the manual trigger — useful for one-off
// scoring jobs and for testing rule predicates without waiting for the
// polling scheduler tick.
//
// Delegates to the same eval_runner.Runner.ExecuteSamplingRule the
// polling scheduler uses, so manual and automatic runs produce
// identical output (same scorers, same sampling-hash decision, same
// score-recorder path). Returns 503 if start_api hasn't wired the
// runner (e.g. CE build without ClickHouse).
func (s *EvalServer) RunSamplingEvalRuleNow(ctx context.Context, req *connect.Request[datasetspb.RunSamplingEvalRuleNowRequest]) (*connect.Response[datasetspb.RunSamplingEvalRuleNowResponse], error) {
	if s.samplingRunner == nil {
		return nil, connect.NewError(connect.CodeUnavailable,
			fmt.Errorf("sampling runner not configured — start_api must call SetSamplingRunner (likely missing ClickHouse connection)"))
	}

	tenantID := getTenantID(ctx, req.Msg.GetTenantId())
	ctx = ensureTenantSchema(ctx, tenantID)

	summary, runErr := s.samplingRunner.ExecuteSamplingRule(ctx, tenantID, req.Msg.GetId())
	if summary == nil {
		summary = &eval_runner.SamplingRunSummary{}
	}
	if runErr != nil && summary.ErrorMessage == "" {
		summary.ErrorMessage = runErr.Error()
	}

	return connect.NewResponse(&datasetspb.RunSamplingEvalRuleNowResponse{
		TracesMatched:  int32(summary.TracesMatched),
		TracesSampled:  int32(summary.TracesSampled),
		ScoresRecorded: int32(summary.ScoresRecorded),
		Error:          summary.ErrorMessage,
	}), nil
}

func samplingRuleToProto(rec *eval_runner.SamplingEvalRuleRecord) *datasetspb.SamplingEvalRule {
	out := &datasetspb.SamplingEvalRule{
		Id:                 rec.ID,
		TenantId:           rec.TenantID,
		Name:               rec.Name,
		Description:        rec.Description,
		SampleRate:         rec.SampleRate,
		ScorerConfigIds:    []string(rec.ScorerConfigIDs),
		LookbackSeconds:    int32(rec.LookbackSeconds),
		IntervalSeconds:    int32(rec.IntervalSeconds),
		Enabled:            rec.Enabled,
		LastRunTraceCount:  int32(rec.LastRunTraceCount),
		LastRunError:       rec.LastRunError,
		CreatedAt:          timestamppb.New(rec.CreatedAt),
		UpdatedAt:          timestamppb.New(rec.UpdatedAt),
	}
	if len(rec.FilterPredicate) > 0 {
		var asMap map[string]interface{}
		if err := json.Unmarshal(rec.FilterPredicate, &asMap); err == nil {
			if s, err := structpb.NewStruct(asMap); err == nil {
				out.FilterPredicate = s
			}
		}
	}
	if rec.LastRunAt.Valid {
		out.LastRunAt = timestamppb.New(rec.LastRunAt.Time)
	}
	if rec.LastProcessedTraceAt.Valid {
		out.LastProcessedTraceAt = timestamppb.New(rec.LastProcessedTraceAt.Time)
	}
	return out
}
