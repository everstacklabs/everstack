package commands

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/database"
)

// PostCommitError reports a failure after domain events were durably written.
// Callers must not destroy resources referenced by those events because a
// repair or replay still needs them.
type PostCommitError struct{ Err error }

func (e *PostCommitError) Error() string {
	return fmt.Sprintf("post-commit event handling failed: %v", e.Err)
}
func (e *PostCommitError) Unwrap() error { return e.Err }

func EventWasPersisted(err error) bool {
	var postCommit *PostCommitError
	return errors.As(err, &postCommit)
}

// Command represents a write operation intent.
type Command interface {
	// AggregateID returns the unique identifier of the aggregate this command targets.
	AggregateID() string
	// CommandType returns the type/name of this command.
	CommandType() string
	// Validate performs basic validation of command data.
	Validate() error
}

// CommandHandler processes commands and produces events.
type CommandHandler interface {
	// Handle processes a command and returns the events that should be persisted.
	Handle(ctx context.Context, cmd Command) ([]database.Event, error)
	// CommandType returns the command type this handler can process.
	CommandType() string
}

// CommandBus coordinates command handling and event persistence.
type CommandBus interface {
	// Dispatch routes a command to its handler and persists resulting events.
	Dispatch(ctx context.Context, cmd Command) error
	// RegisterHandler registers a command handler for a specific command type.
	RegisterHandler(handler CommandHandler)
}

// BaseCommand provides common command functionality.
type BaseCommand struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	UserID    string    `json:"user_id,omitempty"`
	APIKey    string    `json:"api_key,omitempty"`
	TraceID   string    `json:"trace_id,omitempty"`
}

// GetID returns the command identifier.
func (c BaseCommand) GetID() string { return c.ID }

// GetTimestamp returns when the command was created.
func (c BaseCommand) GetTimestamp() time.Time { return c.Timestamp }

// GetUserID returns the user who issued the command.
func (c BaseCommand) GetUserID() string { return c.UserID }

// GetAPIKey returns the API key used for the command.
func (c BaseCommand) GetAPIKey() string { return c.APIKey }

// GetTraceID returns the trace ID for distributed tracing.
func (c BaseCommand) GetTraceID() string { return c.TraceID }
