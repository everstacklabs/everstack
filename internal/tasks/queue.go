package tasks

import (
	"context"
	"time"
)

// TaskStatus represents the lifecycle state of a queued task.
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusCancelled TaskStatus = "cancelled"
)

// Task represents a queued task for async execution.
type Task struct {
	ID            string            `json:"id"`
	Type          string            `json:"type"`
	TenantID      string            `json:"tenant_id"`
	Status        TaskStatus        `json:"status"`
	Payload       []byte            `json:"payload"`
	Result        []byte            `json:"result,omitempty"`
	Error         string            `json:"error,omitempty"`
	Priority      int               `json:"priority"`
	MaxRetries    int               `json:"max_retries"`
	RetryCount    int               `json:"retry_count"`
	Timeout       time.Duration     `json:"timeout"`
	CallbackURL   string            `json:"callback_url,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	StartedAt     *time.Time        `json:"started_at,omitempty"`
	CompletedAt   *time.Time        `json:"completed_at,omitempty"`
	WorkerID      string            `json:"worker_id,omitempty"`
}

// TaskQueue defines the interface for task queue backends.
//
// Every by-id method (Complete / Fail / Cancel / GetTask / Subscribe)
// takes a tenantID. Pre-fix the postgres backend ran queries with
// `WHERE id = $1` and trusted the caller to only ever ask about a
// task it owned — but a buggy or compromised dispatcher could mark
// or read a task across tenants. The id+tenant_id pair is the trust
// anchor.
type TaskQueue interface {
	// Enqueue adds a new task to the queue.
	Enqueue(ctx context.Context, task *Task) error
	// Dequeue retrieves the next available task for processing.
	// Blocks until a task is available or context is cancelled.
	Dequeue(ctx context.Context, taskType string) (*Task, error)
	// Complete marks a task as completed with the given result, scoped to tenant.
	Complete(ctx context.Context, taskID, tenantID string, result []byte) error
	// Fail marks a task as failed with the given error, scoped to tenant.
	Fail(ctx context.Context, taskID, tenantID, errMsg string) error
	// Cancel marks a task as cancelled, scoped to tenant.
	Cancel(ctx context.Context, taskID, tenantID string) error
	// GetTask retrieves a task by ID, scoped to tenant.
	GetTask(ctx context.Context, taskID, tenantID string) (*Task, error)
	// Subscribe registers a callback for task completion events,
	// scoped to tenant (the polling loop uses the same scoped GetTask).
	Subscribe(ctx context.Context, taskID, tenantID string, callback func(*Task)) error
	// ListTasks lists tasks with optional filtering. opts.TenantID is mandatory.
	ListTasks(ctx context.Context, opts ListTasksOptions) ([]*Task, error)
}

// ListTasksOptions configures task listing.
type ListTasksOptions struct {
	TenantID string
	Type     string
	Status   TaskStatus
	Limit    int
	Offset   int
}

// TaskHandler processes a specific type of task.
type TaskHandler interface {
	// TaskType returns the task type this handler processes.
	TaskType() string
	// Handle processes a task and returns the result.
	Handle(ctx context.Context, task *Task) ([]byte, error)
}
