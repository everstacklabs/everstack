package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// PostgresTaskQueue implements TaskQueue using PostgreSQL for persistence.
// Used as fallback when Redis is not available.
type PostgresTaskQueue struct {
	db           *sqlx.DB
	subscribers  map[string][]func(*Task)
	mu           sync.RWMutex
	pollInterval time.Duration
}

// NewPostgresTaskQueue creates a new PostgreSQL-backed task queue.
func NewPostgresTaskQueue(db *sqlx.DB) *PostgresTaskQueue {
	return &PostgresTaskQueue{
		db:           db,
		subscribers:  make(map[string][]func(*Task)),
		pollInterval: 1 * time.Second,
	}
}

func (q *PostgresTaskQueue) Enqueue(ctx context.Context, task *Task) error {
	if task.ID == "" {
		task.ID = uuid.New().String()
	}
	task.Status = TaskStatusPending
	task.CreatedAt = time.Now()

	metadataJSON, _ := json.Marshal(task.Metadata)

	_, err := q.db.ExecContext(ctx,
		`INSERT INTO agent_jobs
		 (id, type, tenant_id, status, payload, priority, max_retries, timeout_seconds, callback_url, metadata, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		task.ID, task.Type, task.TenantID, string(task.Status),
		task.Payload, task.Priority, task.MaxRetries,
		int(task.Timeout.Seconds()), task.CallbackURL, metadataJSON, task.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to enqueue task: %w", err)
	}

	logger.WithFields("task_id", task.ID, "type", task.Type).Debug("task enqueued (postgres)")
	return nil
}

func (q *PostgresTaskQueue) Dequeue(ctx context.Context, taskType string) (*Task, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Use SELECT FOR UPDATE SKIP LOCKED for safe concurrent dequeue
		var row taskRow
		err := q.db.GetContext(ctx, &row,
			`UPDATE agent_jobs
			 SET status = 'running', started_at = NOW(), worker_id = $1
			 WHERE id = (
			     SELECT id FROM agent_jobs
			     WHERE type = $2 AND status = 'pending'
			     ORDER BY priority DESC, created_at ASC
			     LIMIT 1
			     FOR UPDATE SKIP LOCKED
			 )
			 RETURNING id, type, tenant_id, status, payload, result, error, priority,
			           max_retries, retry_count, timeout_seconds, callback_url, metadata,
			           created_at, started_at, completed_at, worker_id`,
			uuid.New().String()[:8], taskType,
		)
		if err != nil {
			if err == sql.ErrNoRows {
				time.Sleep(q.pollInterval)
				continue
			}
			return nil, fmt.Errorf("failed to dequeue task: %w", err)
		}

		return row.toTask(), nil
	}
}

func (q *PostgresTaskQueue) Complete(ctx context.Context, taskID, tenantID string, result []byte) error {
	if tenantID == "" {
		return fmt.Errorf("Complete: tenant id is required")
	}
	_, err := q.db.ExecContext(ctx,
		`UPDATE agent_jobs SET status = 'completed', result = $1, completed_at = NOW() WHERE id = $2 AND tenant_id = $3`,
		result, taskID, tenantID,
	)
	if err != nil {
		return fmt.Errorf("failed to complete task: %w", err)
	}

	// Notify in-process subscribers
	task, _ := q.GetTask(ctx, taskID, tenantID)
	if task != nil {
		q.notifySubscribers(taskID, task)
	}

	return nil
}

func (q *PostgresTaskQueue) Fail(ctx context.Context, taskID, tenantID, errMsg string) error {
	if tenantID == "" {
		return fmt.Errorf("Fail: tenant id is required")
	}
	_, err := q.db.ExecContext(ctx,
		`UPDATE agent_jobs SET status = 'failed', error = $1, completed_at = NOW() WHERE id = $2 AND tenant_id = $3`,
		errMsg, taskID, tenantID,
	)
	if err != nil {
		return fmt.Errorf("failed to mark task as failed: %w", err)
	}

	task, _ := q.GetTask(ctx, taskID, tenantID)
	if task != nil {
		q.notifySubscribers(taskID, task)
	}

	return nil
}

func (q *PostgresTaskQueue) Cancel(ctx context.Context, taskID, tenantID string) error {
	if tenantID == "" {
		return fmt.Errorf("Cancel: tenant id is required")
	}
	_, err := q.db.ExecContext(ctx,
		`UPDATE agent_jobs SET status = 'cancelled', completed_at = NOW() WHERE id = $1 AND tenant_id = $2`,
		taskID, tenantID,
	)
	if err != nil {
		return fmt.Errorf("failed to cancel task: %w", err)
	}
	return nil
}

func (q *PostgresTaskQueue) GetTask(ctx context.Context, taskID, tenantID string) (*Task, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("task %s not found", taskID)
	}
	var row taskRow
	err := q.db.GetContext(ctx, &row,
		`SELECT id, type, tenant_id, status, payload, result, error, priority,
		        max_retries, retry_count, timeout_seconds, callback_url, metadata,
		        created_at, started_at, completed_at, worker_id
		 FROM agent_jobs WHERE id = $1 AND tenant_id = $2`, taskID, tenantID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("task %s not found", taskID)
		}
		return nil, fmt.Errorf("failed to get task: %w", err)
	}
	return row.toTask(), nil
}

func (q *PostgresTaskQueue) Subscribe(ctx context.Context, taskID, tenantID string, callback func(*Task)) error {
	q.mu.Lock()
	q.subscribers[taskID] = append(q.subscribers[taskID], callback)
	q.mu.Unlock()

	// Poll for completion in background
	go func() {
		ticker := time.NewTicker(q.pollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				task, err := q.GetTask(ctx, taskID, tenantID)
				if err != nil {
					continue
				}
				if task.Status == TaskStatusCompleted || task.Status == TaskStatusFailed || task.Status == TaskStatusCancelled {
					callback(task)
					return
				}
			}
		}
	}()

	return nil
}

func (q *PostgresTaskQueue) ListTasks(ctx context.Context, opts ListTasksOptions) ([]*Task, error) {
	// Pre-fix this ran `WHERE 1=1` and ignored opts.TenantID — every
	// tenant's jobs leaked through. Tenant scope is now mandatory.
	if opts.TenantID == "" {
		return nil, nil
	}
	query := `SELECT id, type, tenant_id, status, payload, result, error, priority,
	                 max_retries, retry_count, timeout_seconds, callback_url, metadata,
	                 created_at, started_at, completed_at, worker_id
	          FROM agent_jobs WHERE tenant_id = $1`
	args := []interface{}{opts.TenantID}
	argIdx := 2

	if opts.Type != "" {
		query += fmt.Sprintf(" AND type = $%d", argIdx)
		args = append(args, opts.Type)
		argIdx++
	}
	if opts.Status != "" {
		query += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, string(opts.Status))
		argIdx++
	}

	query += " ORDER BY created_at DESC"

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, opts.Limit)
		argIdx++
	}
	if opts.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argIdx)
		args = append(args, opts.Offset)
	}

	var rows []taskRow
	if err := q.db.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	tasks := make([]*Task, len(rows))
	for i := range rows {
		tasks[i] = rows[i].toTask()
	}
	return tasks, nil
}

func (q *PostgresTaskQueue) notifySubscribers(taskID string, task *Task) {
	q.mu.RLock()
	subs := q.subscribers[taskID]
	q.mu.RUnlock()

	for _, cb := range subs {
		go cb(task)
	}

	q.mu.Lock()
	delete(q.subscribers, taskID)
	q.mu.Unlock()
}

// taskRow maps to the agent_jobs table.
type taskRow struct {
	ID             string         `db:"id"`
	Type           string         `db:"type"`
	TenantID       string         `db:"tenant_id"`
	Status         string         `db:"status"`
	Payload        []byte         `db:"payload"`
	Result         []byte         `db:"result"`
	Error          sql.NullString `db:"error"`
	Priority       int            `db:"priority"`
	MaxRetries     int            `db:"max_retries"`
	RetryCount     int            `db:"retry_count"`
	TimeoutSeconds int            `db:"timeout_seconds"`
	CallbackURL    sql.NullString `db:"callback_url"`
	Metadata       []byte         `db:"metadata"`
	CreatedAt      time.Time      `db:"created_at"`
	StartedAt      sql.NullTime   `db:"started_at"`
	CompletedAt    sql.NullTime   `db:"completed_at"`
	WorkerID       sql.NullString `db:"worker_id"`
}

func (r *taskRow) toTask() *Task {
	task := &Task{
		ID:         r.ID,
		Type:       r.Type,
		TenantID:   r.TenantID,
		Status:     TaskStatus(r.Status),
		Payload:    r.Payload,
		Result:     r.Result,
		Priority:   r.Priority,
		MaxRetries: r.MaxRetries,
		RetryCount: r.RetryCount,
		Timeout:    time.Duration(r.TimeoutSeconds) * time.Second,
		CreatedAt:  r.CreatedAt,
	}

	if r.Error.Valid {
		task.Error = r.Error.String
	}
	if r.CallbackURL.Valid {
		task.CallbackURL = r.CallbackURL.String
	}
	if r.StartedAt.Valid {
		task.StartedAt = &r.StartedAt.Time
	}
	if r.CompletedAt.Valid {
		task.CompletedAt = &r.CompletedAt.Time
	}
	if r.WorkerID.Valid {
		task.WorkerID = r.WorkerID.String
	}
	if len(r.Metadata) > 0 {
		json.Unmarshal(r.Metadata, &task.Metadata)
	}

	return task
}
