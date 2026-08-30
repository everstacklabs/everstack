package prompts

import (
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/google/uuid"
)

// PromptMessagePayload is one chat-template message captured in a version.
type PromptMessagePayload struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// --- Prompt Commands ---

// CreatePromptCommand creates a new prompt, optionally with an initial version.
type CreatePromptCommand struct {
	commands.BaseCommand
	TenantID    string   `json:"tenant_id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
	// Initial version content; version 1 is created when non-empty.
	Messages      []PromptMessagePayload `json:"messages,omitempty"`
	Config        map[string]interface{} `json:"config,omitempty"`
	CommitMessage string                 `json:"commit_message,omitempty"`
	// VersionID is pre-generated so the API layer can echo it back.
	VersionID string `json:"version_id"`
}

func NewCreatePromptCommand(
	tenantID, name, description, userID, traceID string,
	tags []string,
	messages []PromptMessagePayload,
	config map[string]interface{},
	commitMessage string,
) *CreatePromptCommand {
	return &CreatePromptCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		TenantID:      tenantID,
		Name:          name,
		Description:   description,
		Tags:          tags,
		Messages:      messages,
		Config:        config,
		CommitMessage: commitMessage,
		VersionID:     uuid.New().String(),
	}
}

func (c CreatePromptCommand) AggregateID() string { return c.ID }
func (c CreatePromptCommand) CommandType() string { return "CreatePrompt" }
func (c CreatePromptCommand) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	return validateMessages(c.Messages)
}

// UpdatePromptCommand updates prompt metadata (not version content).
type UpdatePromptCommand struct {
	commands.BaseCommand
	PromptID    string    `json:"prompt_id"`
	TenantID    string    `json:"tenant_id"`
	Name        *string   `json:"name,omitempty"`
	Description *string   `json:"description,omitempty"`
	Tags        *[]string `json:"tags,omitempty"`
}

func NewUpdatePromptCommand(promptID, tenantID, userID, traceID string) *UpdatePromptCommand {
	return &UpdatePromptCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		PromptID: promptID,
		TenantID: tenantID,
	}
}

func (c UpdatePromptCommand) AggregateID() string { return c.PromptID }
func (c UpdatePromptCommand) CommandType() string { return "UpdatePrompt" }
func (c UpdatePromptCommand) Validate() error {
	if c.PromptID == "" {
		return fmt.Errorf("prompt_id cannot be empty")
	}
	return nil
}

// DeletePromptCommand archives a prompt.
type DeletePromptCommand struct {
	commands.BaseCommand
	PromptID string `json:"prompt_id"`
	TenantID string `json:"tenant_id"`
}

func NewDeletePromptCommand(promptID, tenantID, userID, traceID string) *DeletePromptCommand {
	return &DeletePromptCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		PromptID: promptID,
		TenantID: tenantID,
	}
}

func (c DeletePromptCommand) AggregateID() string { return c.PromptID }
func (c DeletePromptCommand) CommandType() string { return "DeletePrompt" }
func (c DeletePromptCommand) Validate() error {
	if c.PromptID == "" {
		return fmt.Errorf("prompt_id cannot be empty")
	}
	return nil
}

// --- Version Commands ---

// CreatePromptVersionCommand snapshots new content as the next version.
// Version is assigned by the API layer (current latest + 1); the unique
// (prompt_id, version) constraint backstops concurrent writers.
type CreatePromptVersionCommand struct {
	commands.BaseCommand
	PromptID      string                 `json:"prompt_id"`
	TenantID      string                 `json:"tenant_id"`
	Version       int                    `json:"version"`
	Messages      []PromptMessagePayload `json:"messages"`
	Config        map[string]interface{} `json:"config,omitempty"`
	CommitMessage string                 `json:"commit_message,omitempty"`
	Labels        []string               `json:"labels,omitempty"`
}

func NewCreatePromptVersionCommand(
	promptID, tenantID, userID, traceID string,
	version int,
	messages []PromptMessagePayload,
	config map[string]interface{},
	commitMessage string,
	labels []string,
) *CreatePromptVersionCommand {
	return &CreatePromptVersionCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		PromptID:      promptID,
		TenantID:      tenantID,
		Version:       version,
		Messages:      messages,
		Config:        config,
		CommitMessage: commitMessage,
		Labels:        labels,
	}
}

func (c CreatePromptVersionCommand) AggregateID() string { return c.PromptID }
func (c CreatePromptVersionCommand) CommandType() string { return "CreatePromptVersion" }
func (c CreatePromptVersionCommand) Validate() error {
	if c.PromptID == "" {
		return fmt.Errorf("prompt_id cannot be empty")
	}
	if c.Version < 1 {
		return fmt.Errorf("version must be >= 1")
	}
	if len(c.Messages) == 0 {
		return fmt.Errorf("messages cannot be empty")
	}
	return validateMessages(c.Messages)
}

// SetPromptLabelsCommand replaces the label set of one version; supplied
// labels are removed from sibling versions of the same prompt.
type SetPromptLabelsCommand struct {
	commands.BaseCommand
	PromptID string   `json:"prompt_id"`
	TenantID string   `json:"tenant_id"`
	Version  int      `json:"version"`
	Labels   []string `json:"labels"`
}

func NewSetPromptLabelsCommand(promptID, tenantID, userID, traceID string, version int, labels []string) *SetPromptLabelsCommand {
	return &SetPromptLabelsCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		PromptID: promptID,
		TenantID: tenantID,
		Version:  version,
		Labels:   labels,
	}
}

func (c SetPromptLabelsCommand) AggregateID() string { return c.PromptID }
func (c SetPromptLabelsCommand) CommandType() string { return "SetPromptLabels" }
func (c SetPromptLabelsCommand) Validate() error {
	if c.PromptID == "" {
		return fmt.Errorf("prompt_id cannot be empty")
	}
	if c.Version < 1 {
		return fmt.Errorf("version must be >= 1")
	}
	return nil
}

func validateMessages(messages []PromptMessagePayload) error {
	for _, m := range messages {
		switch m.Role {
		case "system", "user", "assistant":
		default:
			return fmt.Errorf("invalid message role: %q (must be system, user, or assistant)", m.Role)
		}
	}
	return nil
}
