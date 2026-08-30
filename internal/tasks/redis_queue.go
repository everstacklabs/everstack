package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/redis/go-redis/v9"
)

const (
	taskKeyPrefix    = "everstack:tasks:"
	taskQueuePrefix  = "everstack:task_queue:"
	taskResultPrefix = "everstack:task_result:"
	taskTTL          = 24 * time.Hour
)

// RedisTaskQueue implements TaskQueue using Redis.
type RedisTaskQueue struct {
	client      *redis.Client
	subscribers map[string][]func(*Task)
	mu          sync.RWMutex
}

// NewRedisTaskQueue creates a new Redis-backed task queue.
func NewRedisTaskQueue(client *redis.Client) *RedisTaskQueue {
	return &RedisTaskQueue{
		client:      client,
		subscribers: make(map[string][]func(*Task)),
	}
}

func (q *RedisTaskQueue) Enqueue(ctx context.Context, task *Task) error {
	if task.ID == "" {
		task.ID = uuid.New().String()
	}
	task.Status = TaskStatusPending
	task.CreatedAt = time.Now()

	data, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("failed to marshal task: %w", err)
	}

	// Store task data
	taskKey := taskKeyPrefix + task.ID
	if err := q.client.Set(ctx, taskKey, data, taskTTL).Err(); err != nil {
		return fmt.Errorf("failed to store task: %w", err)
	}

	// Push to queue
	queueKey := taskQueuePrefix + task.Type
	if err := q.client.LPush(ctx, queueKey, task.ID).Err(); err != nil {
		return fmt.Errorf("failed to enqueue task: %w", err)
	}

	logger.WithFields("task_id", task.ID, "type", task.Type, "tenant_id", task.TenantID).
		Debug("task enqueued")

	return nil
}

func (q *RedisTaskQueue) Dequeue(ctx context.Context, taskType string) (*Task, error) {
	queueKey := taskQueuePrefix + taskType

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Block-pop with 1 second timeout to allow context checking
		result, err := q.client.BRPop(ctx, 1*time.Second, queueKey).Result()
		if err != nil {
			if err == redis.Nil {
				continue // No task available, retry
			}
			return nil, fmt.Errorf("failed to dequeue task: %w", err)
		}

		if len(result) < 2 {
			continue
		}

		taskID := result[1]
		// Internal infra path — we don't yet know the tenant id (it's
		// stored inside the Task struct we're about to load). Use the
		// raw Get so Dequeue keeps working; once we have the Task we
		// know its TenantID for any subsequent Complete/Fail call.
		task, err := q.getTaskRaw(ctx, taskID)
		if err != nil {
			logger.WithFields("task_id", taskID, "error", err.Error()).
				Warn("dequeue: failed to load task, skipping")
			continue
		}

		// Mark as running
		now := time.Now()
		task.Status = TaskStatusRunning
		task.StartedAt = &now
		task.WorkerID = uuid.New().String()[:8]

		if err := q.saveTask(ctx, task); err != nil {
			return nil, fmt.Errorf("failed to update task status: %w", err)
		}

		return task, nil
	}
}

func (q *RedisTaskQueue) Complete(ctx context.Context, taskID, tenantID string, result []byte) error {
	task, err := q.GetTask(ctx, taskID, tenantID)
	if err != nil {
		return err
	}

	now := time.Now()
	task.Status = TaskStatusCompleted
	task.Result = result
	task.CompletedAt = &now

	if err := q.saveTask(ctx, task); err != nil {
		return err
	}

	// Publish completion event
	q.client.Publish(ctx, taskResultPrefix+taskID, "completed")

	// Notify subscribers
	q.notifySubscribers(taskID, task)

	logger.WithFields("task_id", taskID).Debug("task completed")
	return nil
}

func (q *RedisTaskQueue) Fail(ctx context.Context, taskID, tenantID, errMsg string) error {
	task, err := q.GetTask(ctx, taskID, tenantID)
	if err != nil {
		return err
	}

	now := time.Now()
	task.Status = TaskStatusFailed
	task.Error = errMsg
	task.CompletedAt = &now

	if err := q.saveTask(ctx, task); err != nil {
		return err
	}

	q.client.Publish(ctx, taskResultPrefix+taskID, "failed")
	q.notifySubscribers(taskID, task)

	logger.WithFields("task_id", taskID, "error", errMsg).Debug("task failed")
	return nil
}

func (q *RedisTaskQueue) Cancel(ctx context.Context, taskID, tenantID string) error {
	task, err := q.GetTask(ctx, taskID, tenantID)
	if err != nil {
		return err
	}

	now := time.Now()
	task.Status = TaskStatusCancelled
	task.CompletedAt = &now

	if err := q.saveTask(ctx, task); err != nil {
		return err
	}

	q.client.Publish(ctx, taskResultPrefix+taskID, "cancelled")
	q.notifySubscribers(taskID, task)

	return nil
}

// GetTask reads a task by id and verifies its TenantID matches.
// Pre-fix this returned the row by id alone, which let any caller
// read another tenant's task payload / result.
func (q *RedisTaskQueue) GetTask(ctx context.Context, taskID, tenantID string) (*Task, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("task %s not found", taskID)
	}
	task, err := q.getTaskRaw(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if task.TenantID != tenantID {
		// Same shape as not-found so callers can't distinguish "wrong
		// tenant" from "deleted".
		return nil, fmt.Errorf("task %s not found", taskID)
	}
	return task, nil
}

// getTaskRaw reads a task by id without tenant verification. Internal
// dispatch helper — Dequeue uses it because the tenant id only lives
// inside the Task body. NOT exposed via the TaskQueue interface.
func (q *RedisTaskQueue) getTaskRaw(ctx context.Context, taskID string) (*Task, error) {
	taskKey := taskKeyPrefix + taskID
	data, err := q.client.Get(ctx, taskKey).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, fmt.Errorf("task %s not found", taskID)
		}
		return nil, fmt.Errorf("failed to get task: %w", err)
	}

	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, fmt.Errorf("failed to unmarshal task: %w", err)
	}
	return &task, nil
}

func (q *RedisTaskQueue) Subscribe(ctx context.Context, taskID, tenantID string, callback func(*Task)) error {
	q.mu.Lock()
	q.subscribers[taskID] = append(q.subscribers[taskID], callback)
	q.mu.Unlock()

	// Also subscribe via Redis pub/sub for cross-process notifications
	go func() {
		sub := q.client.Subscribe(ctx, taskResultPrefix+taskID)
		defer sub.Close()

		ch := sub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ch:
				task, err := q.GetTask(ctx, taskID, tenantID)
				if err == nil {
					callback(task)
				}
				return
			}
		}
	}()

	return nil
}

func (q *RedisTaskQueue) ListTasks(ctx context.Context, opts ListTasksOptions) ([]*Task, error) {
	// Fail closed when no tenant scope. Pre-fix the filter only ran
	// `if opts.TenantID != ""` — empty tenant returned every tenant's
	// tasks. The interface now requires tenant_id.
	if opts.TenantID == "" {
		return nil, nil
	}
	// For listing, we scan keys. In production, consider a secondary index.
	pattern := taskKeyPrefix + "*"
	var tasks []*Task

	iter := q.client.Scan(ctx, 0, pattern, 100).Iterator()
	for iter.Next(ctx) {
		data, err := q.client.Get(ctx, iter.Val()).Bytes()
		if err != nil {
			continue
		}

		var task Task
		if err := json.Unmarshal(data, &task); err != nil {
			continue
		}

		// Apply filters — tenant predicate is mandatory.
		if task.TenantID != opts.TenantID {
			continue
		}
		if opts.Type != "" && task.Type != opts.Type {
			continue
		}
		if opts.Status != "" && task.Status != opts.Status {
			continue
		}

		tasks = append(tasks, &task)

		if opts.Limit > 0 && len(tasks) >= opts.Limit {
			break
		}
	}

	return tasks, nil
}

func (q *RedisTaskQueue) saveTask(ctx context.Context, task *Task) error {
	data, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("failed to marshal task: %w", err)
	}

	taskKey := taskKeyPrefix + task.ID
	return q.client.Set(ctx, taskKey, data, taskTTL).Err()
}

func (q *RedisTaskQueue) notifySubscribers(taskID string, task *Task) {
	q.mu.RLock()
	subs := q.subscribers[taskID]
	q.mu.RUnlock()

	for _, cb := range subs {
		go cb(task)
	}

	// Clean up subscribers
	q.mu.Lock()
	delete(q.subscribers, taskID)
	q.mu.Unlock()
}
