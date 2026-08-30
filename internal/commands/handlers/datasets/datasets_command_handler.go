package datasets

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/google/uuid"
)

// errMissingTenant is returned by every dataset / score-config /
// eval-run command handler when the request lacks a tenant id. The
// previous version of resolveTenantID substituted a hardcoded synthetic
// tenant ("a1b2c3d4-...") whenever cmd.TenantID was empty, which is the
// exact "first org in DB" / hardcoded-fallback anti-pattern called out
// in the post-2026-05-06 P0 retro: any unauthenticated or
// tenant-context-less request would silently land its writes in a
// shared synthetic tenant where nobody could ever read them, with the
// added risk that two different requests could collide on the
// synthetic id and cross-contaminate.
//
// Fail closed instead — the upstream handler is responsible for
// propagating contextkeys.GetTenantID(ctx) into cmd.TenantID before
// dispatching the command.
var errMissingTenant = fmt.Errorf("tenant id is required (handler invoked without tenant context)")

func resolveTenantID(tenantID string) (string, error) {
	if tenantID == "" {
		return "", errMissingTenant
	}
	return tenantID, nil
}

// DatasetsCommandHandler handles dataset, dataset item, and score config commands.
type DatasetsCommandHandler struct{}

func NewDatasetsCommandHandler() *DatasetsCommandHandler { return &DatasetsCommandHandler{} }

func (h *DatasetsCommandHandler) CommandType() string {
	return "CreateDataset|UpdateDataset|DeleteDataset|CreateDatasetItem|CreateDatasetItemBatch|UpdateDatasetItem|DeleteDatasetItem|CreateScoreConfig|UpdateScoreConfig|DeleteScoreConfig"
}

func (h *DatasetsCommandHandler) Handle(ctx context.Context, cmd commands.Command) ([]database.Event, error) {
	switch c := cmd.(type) {
	case *CreateDatasetCommand:
		return h.handleCreateDataset(ctx, c)
	case *UpdateDatasetCommand:
		return h.handleUpdateDataset(ctx, c)
	case *DeleteDatasetCommand:
		return h.handleDeleteDataset(ctx, c)
	case *CreateDatasetItemCommand:
		return h.handleCreateDatasetItem(ctx, c)
	case *CreateDatasetItemBatchCommand:
		return h.handleCreateDatasetItemBatch(ctx, c)
	case *UpdateDatasetItemCommand:
		return h.handleUpdateDatasetItem(ctx, c)
	case *DeleteDatasetItemCommand:
		return h.handleDeleteDatasetItem(ctx, c)
	case *CreateScoreConfigCommand:
		return h.handleCreateScoreConfig(ctx, c)
	case *UpdateScoreConfigCommand:
		return h.handleUpdateScoreConfig(ctx, c)
	case *DeleteScoreConfigCommand:
		return h.handleDeleteScoreConfig(ctx, c)
	default:
		return nil, fmt.Errorf("unsupported command type")
	}
}

func (h *DatasetsCommandHandler) handleCreateDataset(ctx context.Context, cmd *CreateDatasetCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := resolveTenantID(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"name", cmd.Name,
		"tenant_id", tenantID,
		"user_id", cmd.UserID,
		"correlation_id", correlationID,
	).Debug("processing dataset create command")

	payload := map[string]interface{}{
		"id":             uuid.New().String(),
		"tenant_id":      tenantID,
		"name":           cmd.Name,
		"description":    cmd.Description,
		"metadata":       cmd.Metadata,
		"created_at":     now.Format(time.RFC3339),
		"updated_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "dataset.created",
		Stream:    "datasets",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *DatasetsCommandHandler) handleUpdateDataset(ctx context.Context, cmd *UpdateDatasetCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := resolveTenantID(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"dataset_id", cmd.DatasetID,
		"tenant_id", tenantID,
		"user_id", cmd.UserID,
		"correlation_id", correlationID,
	).Debug("processing dataset update command")

	payload := map[string]interface{}{
		"id":             cmd.DatasetID,
		"tenant_id":      tenantID,
		"updated_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}
	if cmd.Name != nil {
		payload["name"] = *cmd.Name
	}
	if cmd.Description != nil {
		payload["description"] = *cmd.Description
	}
	if cmd.Metadata != nil {
		payload["metadata"] = cmd.Metadata
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "dataset.updated",
		Stream:    "datasets",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *DatasetsCommandHandler) handleDeleteDataset(ctx context.Context, cmd *DeleteDatasetCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := resolveTenantID(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"dataset_id", cmd.DatasetID,
		"tenant_id", tenantID,
		"user_id", cmd.UserID,
		"correlation_id", correlationID,
	).Debug("processing dataset delete command")

	payload := map[string]interface{}{
		"id":             cmd.DatasetID,
		"tenant_id":      tenantID,
		"deleted_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "dataset.deleted",
		Stream:    "datasets",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *DatasetsCommandHandler) handleCreateDatasetItem(ctx context.Context, cmd *CreateDatasetItemCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := resolveTenantID(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"dataset_id", cmd.DatasetID,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing dataset item create command")

	payload := map[string]interface{}{
		"id":                    uuid.New().String(),
		"dataset_id":            cmd.DatasetID,
		"tenant_id":             tenantID,
		"input":                 cmd.Input,
		"expected_output":       cmd.ExpectedOutput,
		"metadata":              cmd.Metadata,
		"source_trace_id":       cmd.SourceTraceID,
		"source_observation_id": cmd.SourceObservationID,
		"status":                "active",
		"created_at":            now.Format(time.RFC3339),
		"updated_at":            now.Format(time.RFC3339),
		"correlation_id":        correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "dataset_item.created",
		Stream:    "datasets",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *DatasetsCommandHandler) handleCreateDatasetItemBatch(ctx context.Context, cmd *CreateDatasetItemBatchCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := resolveTenantID(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"dataset_id", cmd.DatasetID,
		"count", len(cmd.Items),
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing dataset item batch create command")

	var events []database.Event
	for _, item := range cmd.Items {
		payload := map[string]interface{}{
			"id":                    uuid.New().String(),
			"dataset_id":            cmd.DatasetID,
			"tenant_id":             tenantID,
			"input":                 item.Input,
			"expected_output":       item.ExpectedOutput,
			"metadata":              item.Metadata,
			"source_trace_id":       item.SourceTraceID,
			"source_observation_id": item.SourceObservationID,
			"status":                "active",
			"created_at":            now.Format(time.RFC3339),
			"updated_at":            now.Format(time.RFC3339),
			"correlation_id":        correlationID,
		}

		data, _ := json.Marshal(payload)
		events = append(events, database.Event{
			ID:        uuid.New().String(),
			Type:      "dataset_item.created",
			Stream:    "datasets",
			Payload:   data,
			CreatedAt: now.Unix(),
		})
	}

	return events, nil
}

func (h *DatasetsCommandHandler) handleUpdateDatasetItem(ctx context.Context, cmd *UpdateDatasetItemCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := resolveTenantID(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"item_id", cmd.ItemID,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing dataset item update command")

	payload := map[string]interface{}{
		"id":             cmd.ItemID,
		"tenant_id":      tenantID,
		"updated_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}
	if cmd.Input != nil {
		payload["input"] = cmd.Input
	}
	if cmd.ExpectedOutput != nil {
		payload["expected_output"] = cmd.ExpectedOutput
	}
	if cmd.Metadata != nil {
		payload["metadata"] = cmd.Metadata
	}
	if cmd.Status != nil {
		payload["status"] = *cmd.Status
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "dataset_item.updated",
		Stream:    "datasets",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *DatasetsCommandHandler) handleDeleteDatasetItem(ctx context.Context, cmd *DeleteDatasetItemCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := resolveTenantID(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"item_id", cmd.ItemID,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing dataset item delete command")

	payload := map[string]interface{}{
		"id":             cmd.ItemID,
		"tenant_id":      tenantID,
		"deleted_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "dataset_item.deleted",
		Stream:    "datasets",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *DatasetsCommandHandler) handleCreateScoreConfig(ctx context.Context, cmd *CreateScoreConfigCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := resolveTenantID(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"name", cmd.Name,
		"data_type", cmd.DataType,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing score config create command")

	payload := map[string]interface{}{
		"id":              uuid.New().String(),
		"tenant_id":       tenantID,
		"name":            cmd.Name,
		"data_type":       strings.ToUpper(cmd.DataType),
		"description":     cmd.Description,
		"eval_prompt":     cmd.EvalPrompt,
		"eval_model":      cmd.EvalModel,
		"scorer_code":     cmd.ScorerCode,
		"scorer_language": cmd.ScorerLanguage,
		"use_sandbox":     cmd.UseSandbox,
		"dag_definition":  string(cmd.DagDefinition),
		"slug":            cmd.Slug,
		"scorer_type":     cmd.ScorerType,
		"output_type":     cmd.OutputType,
		"messages":        cmd.Messages,
		"model_params":    cmd.ModelParams,
		"choice_scores":   cmd.ChoiceScores,
		"use_cot":         cmd.UseCot,
		"is_archived":     false,
		"created_at":      now.Format(time.RFC3339),
		"updated_at":      now.Format(time.RFC3339),
		"correlation_id":  correlationID,
	}
	if cmd.MinValue != nil {
		payload["min_value"] = *cmd.MinValue
	}
	if cmd.MaxValue != nil {
		payload["max_value"] = *cmd.MaxValue
	}
	if cmd.Categories != nil {
		payload["categories"] = cmd.Categories
	}
	if cmd.PassThreshold != nil {
		payload["pass_threshold"] = *cmd.PassThreshold
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "score_config.created",
		Stream:    "datasets",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *DatasetsCommandHandler) handleUpdateScoreConfig(ctx context.Context, cmd *UpdateScoreConfigCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := resolveTenantID(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"score_config_id", cmd.ScoreConfigID,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing score config update command")

	payload := map[string]interface{}{
		"id":             cmd.ScoreConfigID,
		"tenant_id":      tenantID,
		"updated_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}
	if cmd.Name != nil {
		payload["name"] = *cmd.Name
	}
	if cmd.Description != nil {
		payload["description"] = *cmd.Description
	}
	if cmd.MinValue != nil {
		payload["min_value"] = *cmd.MinValue
	}
	if cmd.MaxValue != nil {
		payload["max_value"] = *cmd.MaxValue
	}
	if cmd.Categories != nil {
		payload["categories"] = cmd.Categories
	}
	if cmd.DataType != nil {
		payload["data_type"] = strings.ToUpper(*cmd.DataType)
	}
	if cmd.EvalPrompt != nil {
		payload["eval_prompt"] = *cmd.EvalPrompt
	}
	if cmd.EvalModel != nil {
		payload["eval_model"] = *cmd.EvalModel
	}
	if cmd.IsArchived != nil {
		payload["is_archived"] = *cmd.IsArchived
	}
	if cmd.ScorerCode != nil {
		payload["scorer_code"] = *cmd.ScorerCode
	}
	if cmd.ScorerLanguage != nil {
		payload["scorer_language"] = *cmd.ScorerLanguage
	}
	if cmd.UseSandbox != nil {
		payload["use_sandbox"] = *cmd.UseSandbox
	}
	if cmd.DagDefinition != nil {
		payload["dag_definition"] = string(cmd.DagDefinition)
	}
	if cmd.Slug != nil {
		payload["slug"] = *cmd.Slug
	}
	if cmd.ScorerType != nil {
		payload["scorer_type"] = *cmd.ScorerType
	}
	if cmd.OutputType != nil {
		payload["output_type"] = *cmd.OutputType
	}
	if cmd.Messages != nil {
		payload["messages"] = cmd.Messages
	}
	if cmd.ModelParams != nil {
		payload["model_params"] = cmd.ModelParams
	}
	if cmd.ChoiceScores != nil {
		payload["choice_scores"] = cmd.ChoiceScores
	}
	if cmd.UseCot != nil {
		payload["use_cot"] = *cmd.UseCot
	}
	if cmd.PassThreshold != nil {
		payload["pass_threshold"] = *cmd.PassThreshold
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "score_config.updated",
		Stream:    "datasets",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *DatasetsCommandHandler) handleDeleteScoreConfig(ctx context.Context, cmd *DeleteScoreConfigCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := resolveTenantID(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"score_config_id", cmd.ScoreConfigID,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing score config delete command")

	payload := map[string]interface{}{
		"id":             cmd.ScoreConfigID,
		"tenant_id":      tenantID,
		"deleted_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "score_config.deleted",
		Stream:    "datasets",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

// EvalCommandHandler handles eval run commands.
type EvalCommandHandler struct{}

func NewEvalCommandHandler() *EvalCommandHandler { return &EvalCommandHandler{} }

func (h *EvalCommandHandler) CommandType() string {
	return "CreateEvalRun|CancelEvalRun|DeleteEvalRun"
}

func (h *EvalCommandHandler) Handle(ctx context.Context, cmd commands.Command) ([]database.Event, error) {
	switch c := cmd.(type) {
	case *CreateEvalRunCommand:
		return h.handleCreateEvalRun(ctx, c)
	case *CancelEvalRunCommand:
		return h.handleCancelEvalRun(ctx, c)
	case *DeleteEvalRunCommand:
		return h.handleDeleteEvalRun(ctx, c)
	default:
		return nil, fmt.Errorf("unsupported command type")
	}
}

func (h *EvalCommandHandler) handleCreateEvalRun(ctx context.Context, cmd *CreateEvalRunCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := resolveTenantID(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"name", cmd.Name,
		"dataset_id", cmd.DatasetID,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing eval run create command")

	payload := map[string]interface{}{
		"id":                 cmd.ID,
		"tenant_id":          tenantID,
		"dataset_id":         cmd.DatasetID,
		"name":               cmd.Name,
		"description":        cmd.Description,
		"status":             "pending",
		"eval_target_type":   cmd.EvalTargetType,
		"eval_target_id":     cmd.EvalTargetID,
		"eval_config":        cmd.EvalConfig,
		"scorer_config_ids":  cmd.ScorerConfigIDs,
		"dataset_version_id": cmd.DatasetVersionID,
		"total_items":        0,
		"completed_items":    0,
		"failed_items":       0,
		"score_summary":      map[string]interface{}{},
		"created_at":         now.Format(time.RFC3339),
		"updated_at":         now.Format(time.RFC3339),
		"correlation_id":     correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "eval_run.created",
		Stream:    "eval_runs",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *EvalCommandHandler) handleCancelEvalRun(ctx context.Context, cmd *CancelEvalRunCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := resolveTenantID(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"eval_run_id", cmd.EvalRunID,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing eval run cancel command")

	payload := map[string]interface{}{
		"id":             cmd.EvalRunID,
		"tenant_id":      tenantID,
		"status":         "cancelled",
		"updated_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "eval_run.cancelled",
		Stream:    "eval_runs",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *EvalCommandHandler) handleDeleteEvalRun(ctx context.Context, cmd *DeleteEvalRunCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID, err := resolveTenantID(cmd.TenantID)
	if err != nil {
		return nil, err
	}

	logger.WithFields(
		"command_id", cmd.ID,
		"eval_run_id", cmd.EvalRunID,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing eval run delete command")

	payload := map[string]interface{}{
		"id":             cmd.EvalRunID,
		"tenant_id":      tenantID,
		"deleted_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "eval_run.deleted",
		Stream:    "eval_runs",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}
