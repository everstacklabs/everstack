package workflows

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/commands"
)

// CreateWorkflowCommand creates a new workflow
type CreateWorkflowCommand struct {
	commands.BaseCommand
	TenantID    string `json:"tenant_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Nodes       []byte `json:"nodes"`    // JSON
	Edges       []byte `json:"edges"`    // JSON
	Viewport    []byte `json:"viewport"` // JSON
}

func NewCreateWorkflowCommand(
	tenantID, name, description string,
	nodes, edges, viewport []byte,
	userID, traceID string,
) *CreateWorkflowCommand {
	return &CreateWorkflowCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:    tenantID,
		Name:        name,
		Description: description,
		Nodes:       nodes,
		Edges:       edges,
		Viewport:    viewport,
	}
}

func (c CreateWorkflowCommand) AggregateID() string { return c.ID }
func (c CreateWorkflowCommand) CommandType() string  { return "CreateWorkflow" }
func (c CreateWorkflowCommand) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	return nil
}

// UpdateWorkflowCommand updates an existing workflow
type UpdateWorkflowCommand struct {
	commands.BaseCommand
	WorkflowID  string  `json:"workflow_id"`
	TenantID    string  `json:"tenant_id"`
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Nodes       []byte  `json:"nodes,omitempty"`    // JSON
	Edges       []byte  `json:"edges,omitempty"`    // JSON
	Viewport    []byte  `json:"viewport,omitempty"` // JSON
	Enabled     *bool   `json:"enabled,omitempty"`
}

func NewUpdateWorkflowCommand(workflowID, tenantID, userID, traceID string) *UpdateWorkflowCommand {
	return &UpdateWorkflowCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		WorkflowID: workflowID,
		TenantID:   tenantID,
	}
}

func (c UpdateWorkflowCommand) AggregateID() string { return c.WorkflowID }
func (c UpdateWorkflowCommand) CommandType() string  { return "UpdateWorkflow" }
func (c UpdateWorkflowCommand) Validate() error {
	if c.WorkflowID == "" {
		return fmt.Errorf("workflow_id cannot be empty")
	}
	return nil
}

// DeleteWorkflowCommand deletes a workflow
type DeleteWorkflowCommand struct {
	commands.BaseCommand
	WorkflowID string `json:"workflow_id"`
	TenantID   string `json:"tenant_id"`
}

func NewDeleteWorkflowCommand(workflowID, tenantID, userID, traceID string) *DeleteWorkflowCommand {
	return &DeleteWorkflowCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		WorkflowID: workflowID,
		TenantID:   tenantID,
	}
}

func (c DeleteWorkflowCommand) AggregateID() string { return c.WorkflowID }
func (c DeleteWorkflowCommand) CommandType() string  { return "DeleteWorkflow" }
func (c DeleteWorkflowCommand) Validate() error {
	if c.WorkflowID == "" {
		return fmt.Errorf("workflow_id cannot be empty")
	}
	return nil
}
