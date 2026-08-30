package annotations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

const selfHostedTenantID = "a1b2c3d4-e5f6-4a5b-8c9d-0e1f2a3b4c5d"

var ErrAnnotationItemPermissionDenied = errors.New("annotation item access denied")

// AnnotationsCommandHandler handles annotation queue create/update/delete commands.
type AnnotationsCommandHandler struct {
	db *sqlx.DB
}

func NewAnnotationsCommandHandler(db ...*sqlx.DB) *AnnotationsCommandHandler {
	var handlerDB *sqlx.DB
	if len(db) > 0 {
		handlerDB = db[0]
	}
	return &AnnotationsCommandHandler{db: handlerDB}
}

func (h *AnnotationsCommandHandler) CommandType() string {
	return "CreateAnnotationQueue|UpdateAnnotationQueue|DeleteAnnotationQueue|AddItemToAnnotationQueue|AddItemsToAnnotationQueueBatch|SubmitAnnotation|SkipAnnotationItem"
}

func (h *AnnotationsCommandHandler) Handle(ctx context.Context, cmd commands.Command) ([]database.Event, error) {
	switch c := cmd.(type) {
	case *CreateQueueCommand:
		return h.handleCreateQueue(ctx, c)
	case *UpdateQueueCommand:
		return h.handleUpdateQueue(ctx, c)
	case *DeleteQueueCommand:
		return h.handleDeleteQueue(ctx, c)
	case *AddItemToQueueCommand:
		return h.handleAddItem(ctx, c)
	case *AddItemsToQueueBatchCommand:
		return h.handleAddItemsBatch(ctx, c)
	case *SubmitAnnotationCommand:
		return h.handleSubmitAnnotation(ctx, c)
	case *SkipItemCommand:
		return h.handleSkipItem(ctx, c)
	default:
		return nil, fmt.Errorf("unsupported command type")
	}
}

func resolveTenantID(tenantID string) string {
	if tenantID == "" {
		return selfHostedTenantID
	}
	return tenantID
}

func (h *AnnotationsCommandHandler) handleCreateQueue(ctx context.Context, cmd *CreateQueueCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID := resolveTenantID(cmd.TenantID)

	logger.WithFields(
		"command_id", cmd.ID,
		"name", cmd.Name,
		"tenant_id", tenantID,
		"user_id", cmd.UserID,
		"correlation_id", correlationID,
	).Debug("processing annotation queue create command")

	payload := map[string]interface{}{
		"id":                   uuid.New().String(),
		"tenant_id":            tenantID,
		"name":                 cmd.Name,
		"description":          cmd.Description,
		"status":               cmd.Status,
		"score_config_ids":     cmd.ScoreConfigIDs,
		"assignment_mode":      cmd.AssignmentMode,
		"annotators":           cmd.Annotators,
		"auto_populate_config": cmd.AutoPopulateConfig,
		"items_pending":        0,
		"items_completed":      0,
		"created_at":           now.Format(time.RFC3339),
		"updated_at":           now.Format(time.RFC3339),
		"correlation_id":       correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "annotation_queue.created",
		Stream:    "annotation_queues",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *AnnotationsCommandHandler) handleUpdateQueue(ctx context.Context, cmd *UpdateQueueCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID := resolveTenantID(cmd.TenantID)

	logger.WithFields(
		"command_id", cmd.ID,
		"queue_id", cmd.QueueID,
		"tenant_id", tenantID,
		"user_id", cmd.UserID,
		"correlation_id", correlationID,
	).Debug("processing annotation queue update command")

	payload := map[string]interface{}{
		"id":             cmd.QueueID,
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
	if cmd.Status != nil {
		payload["status"] = *cmd.Status
	}
	if cmd.ScoreConfigIDs != nil {
		payload["score_config_ids"] = cmd.ScoreConfigIDs
	}
	if cmd.AssignmentMode != nil {
		payload["assignment_mode"] = *cmd.AssignmentMode
	}
	if cmd.Annotators != nil {
		payload["annotators"] = cmd.Annotators
	}
	if cmd.AutoPopulateConfig != nil {
		payload["auto_populate_config"] = *cmd.AutoPopulateConfig
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "annotation_queue.updated",
		Stream:    "annotation_queues",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *AnnotationsCommandHandler) handleDeleteQueue(ctx context.Context, cmd *DeleteQueueCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID := resolveTenantID(cmd.TenantID)

	logger.WithFields(
		"command_id", cmd.ID,
		"queue_id", cmd.QueueID,
		"tenant_id", tenantID,
		"user_id", cmd.UserID,
		"correlation_id", correlationID,
	).Debug("processing annotation queue delete command")

	payload := map[string]interface{}{
		"id":             cmd.QueueID,
		"tenant_id":      tenantID,
		"deleted_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "annotation_queue.deleted",
		Stream:    "annotation_queues",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *AnnotationsCommandHandler) handleAddItem(ctx context.Context, cmd *AddItemToQueueCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID := resolveTenantID(cmd.TenantID)

	logger.WithFields(
		"command_id", cmd.ID,
		"queue_id", cmd.QueueID,
		"trace_id", cmd.TraceID,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing add item to annotation queue command")

	payload := map[string]interface{}{
		"id":             uuid.New().String(),
		"queue_id":       cmd.QueueID,
		"tenant_id":      tenantID,
		"trace_id":       cmd.TraceID,
		"observation_id": cmd.ObservationID,
		"status":         "pending",
		"priority":       cmd.Priority,
		"created_at":     now.Format(time.RFC3339),
		"updated_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "annotation_queue_item.added",
		Stream:    "annotation_queue_items",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *AnnotationsCommandHandler) handleAddItemsBatch(ctx context.Context, cmd *AddItemsToQueueBatchCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID := resolveTenantID(cmd.TenantID)

	logger.WithFields(
		"command_id", cmd.ID,
		"queue_id", cmd.QueueID,
		"item_count", len(cmd.Items),
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing batch add items to annotation queue command")

	var events []database.Event
	for _, item := range cmd.Items {
		payload := map[string]interface{}{
			"id":             uuid.New().String(),
			"queue_id":       cmd.QueueID,
			"tenant_id":      tenantID,
			"trace_id":       item.TraceID,
			"observation_id": item.ObservationID,
			"status":         "pending",
			"priority":       item.Priority,
			"created_at":     now.Format(time.RFC3339),
			"updated_at":     now.Format(time.RFC3339),
			"correlation_id": correlationID,
		}
		data, _ := json.Marshal(payload)
		events = append(events, database.Event{
			ID:        uuid.New().String(),
			Type:      "annotation_queue_item.added",
			Stream:    "annotation_queue_items",
			Payload:   data,
			CreatedAt: now.Unix(),
		})
	}

	return events, nil
}

func (h *AnnotationsCommandHandler) handleSubmitAnnotation(ctx context.Context, cmd *SubmitAnnotationCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID := resolveTenantID(cmd.TenantID)

	logger.WithFields(
		"command_id", cmd.ID,
		"item_id", cmd.ItemID,
		"completed_by", cmd.CompletedBy,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing submit annotation command")

	if h.db != nil {
		result, err := h.db.ExecContext(ctx, `
			UPDATE annotation_queue_items
			SET
				status = 'completed',
				completed_by = $2,
				completed_at = $3::timestamptz,
				updated_at = $4::timestamptz,
				assigned_to = CASE WHEN assigned_to = '' AND $5 <> '' THEN $5 ELSE assigned_to END,
				assigned_at = CASE WHEN assigned_to = '' AND $5 <> '' THEN $3::timestamptz ELSE assigned_at END
			WHERE id = $1
				AND tenant_id = $6
				AND (assigned_to = $5 OR assigned_to = '')
				AND EXISTS (
					SELECT 1
					FROM annotation_queues
					WHERE annotation_queues.id = annotation_queue_items.queue_id
						AND annotation_queues.tenant_id = $6
						AND (
							COALESCE(cardinality(annotation_queues.annotators), 0) = 0
							OR ($5 <> '' AND $5 = ANY(annotation_queues.annotators))
						)
				)
		`, cmd.ItemID, cmd.CompletedBy, now, now, cmd.UserID, tenantID)
		if err != nil {
			return nil, fmt.Errorf("failed to update annotation queue item: %w", err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("failed to verify annotation queue item update: %w", err)
		}
		if rowsAffected == 0 {
			return nil, ErrAnnotationItemPermissionDenied
		}
	}

	// Build score entries for the event
	scoreEntries := make([]map[string]interface{}, 0, len(cmd.Scores))
	for _, score := range cmd.Scores {
		scoreEntries = append(scoreEntries, map[string]interface{}{
			"id":              uuid.New().String(),
			"queue_item_id":   cmd.ItemID,
			"score_config_id": score.ScoreConfigID,
			"score_id":        score.ScoreID,
			"tenant_id":       tenantID,
			"created_at":      now.Format(time.RFC3339),
		})
	}

	payload := map[string]interface{}{
		"item_id":        cmd.ItemID,
		"tenant_id":      tenantID,
		"completed_by":   cmd.CompletedBy,
		"completed_at":   now.Format(time.RFC3339),
		"status":         "completed",
		"scores":         scoreEntries,
		"user_id":        cmd.UserID,
		"updated_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "annotation_queue_item.completed",
		Stream:    "annotation_queue_items",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}

func (h *AnnotationsCommandHandler) handleSkipItem(ctx context.Context, cmd *SkipItemCommand) ([]database.Event, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	correlationID := correlation.GetCorrelationID(ctx)
	now := time.Now()
	tenantID := resolveTenantID(cmd.TenantID)

	logger.WithFields(
		"command_id", cmd.ID,
		"item_id", cmd.ItemID,
		"tenant_id", tenantID,
		"correlation_id", correlationID,
	).Debug("processing skip annotation item command")

	if h.db != nil {
		result, err := h.db.ExecContext(ctx, `
			UPDATE annotation_queue_items
			SET
				status = 'skipped',
				completed_at = $2::timestamptz,
				updated_at = $3::timestamptz,
				assigned_to = CASE WHEN assigned_to = '' AND $4 <> '' THEN $4 ELSE assigned_to END,
				assigned_at = CASE WHEN assigned_to = '' AND $4 <> '' THEN $2::timestamptz ELSE assigned_at END
			WHERE id = $1
				AND tenant_id = $5
				AND (assigned_to = $4 OR assigned_to = '')
				AND EXISTS (
					SELECT 1
					FROM annotation_queues
					WHERE annotation_queues.id = annotation_queue_items.queue_id
						AND annotation_queues.tenant_id = $5
						AND (
							COALESCE(cardinality(annotation_queues.annotators), 0) = 0
							OR ($4 <> '' AND $4 = ANY(annotation_queues.annotators))
						)
				)
		`, cmd.ItemID, now, now, cmd.UserID, tenantID)
		if err != nil {
			return nil, fmt.Errorf("failed to update annotation queue item: %w", err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("failed to verify annotation queue item update: %w", err)
		}
		if rowsAffected == 0 {
			return nil, ErrAnnotationItemPermissionDenied
		}
	}

	payload := map[string]interface{}{
		"item_id":        cmd.ItemID,
		"tenant_id":      tenantID,
		"skipped_by":     cmd.SkippedBy,
		"status":         "skipped",
		"completed_at":   now.Format(time.RFC3339),
		"user_id":        cmd.UserID,
		"updated_at":     now.Format(time.RFC3339),
		"correlation_id": correlationID,
	}

	data, _ := json.Marshal(payload)
	return []database.Event{{
		ID:        uuid.New().String(),
		Type:      "annotation_queue_item.skipped",
		Stream:    "annotation_queue_items",
		Payload:   data,
		CreatedAt: now.Unix(),
	}}, nil
}
