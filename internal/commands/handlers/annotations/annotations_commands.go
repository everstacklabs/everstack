package annotations

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/commands"
)

// CreateQueueCommand creates a new annotation queue.
type CreateQueueCommand struct {
	commands.BaseCommand
	TenantID            string   `json:"tenant_id"`
	Name                string   `json:"name"`
	Description         string   `json:"description"`
	Status              string   `json:"status"`
	ScoreConfigIDs      []string `json:"score_config_ids"`
	AssignmentMode      string   `json:"assignment_mode"`
	Annotators          []string `json:"annotators"`
	AutoPopulateConfig  string   `json:"auto_populate_config"` // JSON string
}

func NewCreateQueueCommand(
	tenantID, name, description, status, assignmentMode string,
	scoreConfigIDs, annotators []string,
	autoPopulateConfig string,
	userID, traceID string,
) *CreateQueueCommand {
	if status == "" {
		status = "active"
	}
	if assignmentMode == "" {
		assignmentMode = "manual"
	}
	return &CreateQueueCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:           tenantID,
		Name:               name,
		Description:        description,
		Status:             status,
		ScoreConfigIDs:     scoreConfigIDs,
		AssignmentMode:     assignmentMode,
		Annotators:         annotators,
		AutoPopulateConfig: autoPopulateConfig,
	}
}

func (c CreateQueueCommand) AggregateID() string { return c.ID }
func (c CreateQueueCommand) CommandType() string  { return "CreateAnnotationQueue" }
func (c CreateQueueCommand) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	if c.Status != "" && c.Status != "active" && c.Status != "paused" && c.Status != "archived" {
		return fmt.Errorf("invalid status: %s (must be active, paused, or archived)", c.Status)
	}
	if c.AssignmentMode != "" && c.AssignmentMode != "manual" && c.AssignmentMode != "round_robin" && c.AssignmentMode != "random" {
		return fmt.Errorf("invalid assignment_mode: %s (must be manual, round_robin, or random)", c.AssignmentMode)
	}
	return nil
}

// UpdateQueueCommand updates an existing annotation queue.
type UpdateQueueCommand struct {
	commands.BaseCommand
	QueueID             string    `json:"queue_id"`
	TenantID            string    `json:"tenant_id"`
	Name                *string   `json:"name,omitempty"`
	Description         *string   `json:"description,omitempty"`
	Status              *string   `json:"status,omitempty"`
	ScoreConfigIDs      []string  `json:"score_config_ids,omitempty"`
	AssignmentMode      *string   `json:"assignment_mode,omitempty"`
	Annotators          []string  `json:"annotators,omitempty"`
	AutoPopulateConfig  *string   `json:"auto_populate_config,omitempty"`
}

func NewUpdateQueueCommand(queueID, tenantID, userID, traceID string) *UpdateQueueCommand {
	return &UpdateQueueCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		QueueID:  queueID,
		TenantID: tenantID,
	}
}

func (c UpdateQueueCommand) AggregateID() string { return c.QueueID }
func (c UpdateQueueCommand) CommandType() string  { return "UpdateAnnotationQueue" }
func (c UpdateQueueCommand) Validate() error {
	if c.QueueID == "" {
		return fmt.Errorf("queue_id cannot be empty")
	}
	if c.Status != nil {
		s := *c.Status
		if s != "active" && s != "paused" && s != "archived" {
			return fmt.Errorf("invalid status: %s (must be active, paused, or archived)", s)
		}
	}
	if c.AssignmentMode != nil {
		m := *c.AssignmentMode
		if m != "manual" && m != "round_robin" && m != "random" {
			return fmt.Errorf("invalid assignment_mode: %s (must be manual, round_robin, or random)", m)
		}
	}
	return nil
}

// DeleteQueueCommand deletes an annotation queue.
type DeleteQueueCommand struct {
	commands.BaseCommand
	QueueID  string `json:"queue_id"`
	TenantID string `json:"tenant_id"`
}

func NewDeleteQueueCommand(queueID, tenantID, userID, traceID string) *DeleteQueueCommand {
	return &DeleteQueueCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		QueueID:  queueID,
		TenantID: tenantID,
	}
}

func (c DeleteQueueCommand) AggregateID() string { return c.QueueID }
func (c DeleteQueueCommand) CommandType() string  { return "DeleteAnnotationQueue" }
func (c DeleteQueueCommand) Validate() error {
	if c.QueueID == "" {
		return fmt.Errorf("queue_id cannot be empty")
	}
	return nil
}

// AddItemToQueueCommand adds a single item to an annotation queue.
type AddItemToQueueCommand struct {
	commands.BaseCommand
	TenantID      string `json:"tenant_id"`
	QueueID       string `json:"queue_id"`
	TraceID       string `json:"trace_id"`
	ObservationID string `json:"observation_id"`
	Priority      int32  `json:"priority"`
}

func NewAddItemToQueueCommand(tenantID, queueID, traceID, observationID string, priority int32, userID, cmdTraceID string) *AddItemToQueueCommand {
	return &AddItemToQueueCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   cmdTraceID,
		},
		TenantID:      tenantID,
		QueueID:       queueID,
		TraceID:       traceID,
		ObservationID: observationID,
		Priority:      priority,
	}
}

func (c AddItemToQueueCommand) AggregateID() string { return c.ID }
func (c AddItemToQueueCommand) CommandType() string  { return "AddItemToAnnotationQueue" }
func (c AddItemToQueueCommand) Validate() error {
	if c.QueueID == "" {
		return fmt.Errorf("queue_id cannot be empty")
	}
	if c.TraceID == "" {
		return fmt.Errorf("trace_id cannot be empty")
	}
	return nil
}

// BatchItem represents a single item in a batch add operation.
type BatchItem struct {
	TraceID       string `json:"trace_id"`
	ObservationID string `json:"observation_id"`
	Priority      int32  `json:"priority"`
}

// AddItemsToQueueBatchCommand adds multiple items to an annotation queue.
type AddItemsToQueueBatchCommand struct {
	commands.BaseCommand
	TenantID string      `json:"tenant_id"`
	QueueID  string      `json:"queue_id"`
	Items    []BatchItem `json:"items"`
}

func NewAddItemsToQueueBatchCommand(tenantID, queueID string, items []BatchItem, userID, traceID string) *AddItemsToQueueBatchCommand {
	return &AddItemsToQueueBatchCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID: tenantID,
		QueueID:  queueID,
		Items:    items,
	}
}

func (c AddItemsToQueueBatchCommand) AggregateID() string { return c.QueueID }
func (c AddItemsToQueueBatchCommand) CommandType() string  { return "AddItemsToAnnotationQueueBatch" }
func (c AddItemsToQueueBatchCommand) Validate() error {
	if c.QueueID == "" {
		return fmt.Errorf("queue_id cannot be empty")
	}
	if len(c.Items) == 0 {
		return fmt.Errorf("items cannot be empty")
	}
	for i, item := range c.Items {
		if item.TraceID == "" {
			return fmt.Errorf("trace_id cannot be empty for item at index %d", i)
		}
	}
	return nil
}

// ScoreEntry represents a score submitted during annotation.
type ScoreEntry struct {
	ScoreConfigID string `json:"score_config_id"`
	ScoreID       string `json:"score_id"`
}

// SubmitAnnotationCommand submits annotation scores for a queue item.
type SubmitAnnotationCommand struct {
	commands.BaseCommand
	TenantID    string       `json:"tenant_id"`
	ItemID      string       `json:"item_id"`
	CompletedBy string       `json:"completed_by"`
	Scores      []ScoreEntry `json:"scores"`
}

func NewSubmitAnnotationCommand(tenantID, itemID, completedBy string, scores []ScoreEntry, userID, traceID string) *SubmitAnnotationCommand {
	return &SubmitAnnotationCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:    tenantID,
		ItemID:      itemID,
		CompletedBy: completedBy,
		Scores:      scores,
	}
}

func (c SubmitAnnotationCommand) AggregateID() string { return c.ItemID }
func (c SubmitAnnotationCommand) CommandType() string  { return "SubmitAnnotation" }
func (c SubmitAnnotationCommand) Validate() error {
	if c.ItemID == "" {
		return fmt.Errorf("item_id cannot be empty")
	}
	if c.CompletedBy == "" {
		return fmt.Errorf("completed_by cannot be empty")
	}
	return nil
}

// SkipItemCommand skips an annotation queue item.
type SkipItemCommand struct {
	commands.BaseCommand
	TenantID  string `json:"tenant_id"`
	ItemID    string `json:"item_id"`
	SkippedBy string `json:"skipped_by"`
}

func NewSkipItemCommand(tenantID, itemID, skippedBy, userID, traceID string) *SkipItemCommand {
	return &SkipItemCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:  tenantID,
		ItemID:    itemID,
		SkippedBy: skippedBy,
	}
}

func (c SkipItemCommand) AggregateID() string { return c.ItemID }
func (c SkipItemCommand) CommandType() string  { return "SkipAnnotationItem" }
func (c SkipItemCommand) Validate() error {
	if c.ItemID == "" {
		return fmt.Errorf("item_id cannot be empty")
	}
	return nil
}
