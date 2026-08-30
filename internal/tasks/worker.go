package tasks

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Worker processes tasks from the queue in the background.
type Worker struct {
	queue    TaskQueue
	handlers map[string]TaskHandler
	mu       sync.RWMutex
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// NewWorker creates a new task worker.
func NewWorker(queue TaskQueue) *Worker {
	return &Worker{
		queue:    queue,
		handlers: make(map[string]TaskHandler),
	}
}

// RegisterHandler registers a handler for a task type.
func (w *Worker) RegisterHandler(handler TaskHandler) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.handlers[handler.TaskType()] = handler
}

// Start begins processing tasks for all registered task types.
func (w *Worker) Start(ctx context.Context) {
	ctx, w.cancel = context.WithCancel(ctx)

	w.mu.RLock()
	taskTypes := make([]string, 0, len(w.handlers))
	for t := range w.handlers {
		taskTypes = append(taskTypes, t)
	}
	w.mu.RUnlock()

	for _, taskType := range taskTypes {
		w.wg.Add(1)
		go w.processLoop(ctx, taskType)
	}

	logger.WithFields("task_types", len(taskTypes)).Info("task worker started")
}

// Stop gracefully stops the worker.
func (w *Worker) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
	logger.Info("task worker stopped")
}

func (w *Worker) processLoop(ctx context.Context, taskType string) {
	defer w.wg.Done()

	logger.WithFields("type", taskType).Debug("worker: processing loop started")

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		task, err := w.queue.Dequeue(ctx, taskType)
		if err != nil {
			if ctx.Err() != nil {
				return // Context cancelled
			}
			logger.WithFields("type", taskType, "error", err.Error()).
				Warn("worker: dequeue error, retrying")
			time.Sleep(1 * time.Second)
			continue
		}

		w.processTask(ctx, task)
	}
}

func (w *Worker) processTask(ctx context.Context, task *Task) {
	w.mu.RLock()
	handler, ok := w.handlers[task.Type]
	w.mu.RUnlock()

	if !ok {
		w.queue.Fail(ctx, task.ID, task.TenantID, fmt.Sprintf("no handler for task type: %s", task.Type))
		return
	}

	// Apply task timeout
	taskCtx := ctx
	if task.Timeout > 0 {
		var cancel context.CancelFunc
		taskCtx, cancel = context.WithTimeout(ctx, task.Timeout)
		defer cancel()
	}

	logger.WithFields("task_id", task.ID, "type", task.Type).
		Debug("worker: processing task")

	result, err := handler.Handle(taskCtx, task)
	if err != nil {
		logger.WithFields("task_id", task.ID, "error", err.Error()).
			Warn("worker: task failed")

		// Check for retry
		if task.RetryCount < task.MaxRetries {
			task.RetryCount++
			task.Status = TaskStatusPending
			if requeueErr := w.queue.Enqueue(ctx, task); requeueErr != nil {
				w.queue.Fail(ctx, task.ID, task.TenantID, err.Error())
			}
			return
		}

		w.queue.Fail(ctx, task.ID, task.TenantID, err.Error())
		return
	}

	w.queue.Complete(ctx, task.ID, task.TenantID, result)

	// Handle callback URL if specified
	if task.CallbackURL != "" {
		go w.sendCallback(task, result)
	}
}

func (w *Worker) sendCallback(task *Task, result []byte) {
	// Callback implementation would use net/http to POST result to CallbackURL.
	// Keeping this as a stub for now as it requires HTTP client setup.
	logger.WithFields("task_id", task.ID, "callback_url", task.CallbackURL).
		Debug("worker: callback URL specified (not yet implemented)")
}
