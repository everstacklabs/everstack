package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// JobStatus represents the current state of an async job.
type JobStatus string

const (
	JobStatusPending   JobStatus = "pending"
	JobStatusRunning   JobStatus = "running"
	JobStatusCompleted JobStatus = "completed"
	JobStatusFailed    JobStatus = "failed"
	JobStatusCancelled JobStatus = "cancelled"
)

// JobRequest describes an async job to enqueue.
type JobRequest struct {
	SessionID string
	AgentID   string
	TenantID  string
	Job       string // Human-readable job description
	RunFunc   func(ctx context.Context) (string, error)
}

// JobResult is the outcome of a completed async job.
type JobResult struct {
	JobID     string
	Status    JobStatus
	Result    string
	Error     string
	StartedAt time.Time
	Duration  time.Duration
}

// JobHandle is returned to the caller after enqueuing a job.
type JobHandle struct {
	JobID    string
	ResultCh <-chan JobResult // buffered(1), written once on completion
	Cancel   context.CancelFunc
}

// JobQueue is the interface for async job execution.
type JobQueue interface {
	Enqueue(ctx context.Context, req JobRequest) (*JobHandle, error)
	Status(ctx context.Context, jobID string) (JobStatus, string, error)
	Cancel(ctx context.Context, jobID string) error
	Shutdown(ctx context.Context) error
	ResultCh() <-chan JobResult
}

// jobEntry tracks a single in-flight job.
type jobEntry struct {
	id        string
	status    JobStatus
	result    string
	err       string
	startedAt time.Time
	cancel    context.CancelFunc
}

// LocalJobQueue executes jobs as goroutines with a concurrency limiter.
type LocalJobQueue struct {
	mu        sync.RWMutex
	jobs      map[string]*jobEntry
	semaphore chan struct{}
	resultCh  chan JobResult // fan-in channel read by the parent loop
	emitter   *Emitter
	wg        sync.WaitGroup
}

// NewLocalJobQueue creates a new local job queue.
func NewLocalJobQueue(maxConcurrent int, emitter *Emitter) *LocalJobQueue {
	if maxConcurrent <= 0 {
		maxConcurrent = 5
	}
	return &LocalJobQueue{
		jobs:      make(map[string]*jobEntry),
		semaphore: make(chan struct{}, maxConcurrent),
		resultCh:  make(chan JobResult, 32),
		emitter:   emitter,
	}
}

// SetEmitter sets the emitter after construction (for deferred wiring).
func (q *LocalJobQueue) SetEmitter(e *Emitter) { q.emitter = e }

// Enqueue adds a job to the queue and starts it when a slot is available.
func (q *LocalJobQueue) Enqueue(ctx context.Context, req JobRequest) (*JobHandle, error) {
	jobID := uuid.New().String()
	jobCtx, cancel := context.WithCancel(ctx)

	entry := &jobEntry{
		id:     jobID,
		status: JobStatusPending,
		cancel: cancel,
	}

	q.mu.Lock()
	q.jobs[jobID] = entry
	q.mu.Unlock()

	resultCh := make(chan JobResult, 1)

	if q.emitter != nil {
		q.emitter.Emit(Event{
			Type:      EventJobEnqueued,
			SessionID: req.SessionID,
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"job_id":   jobID,
				"agent_id": req.AgentID,
				"job":      truncateString(req.Job, 200),
			},
		})
	}

	q.wg.Add(1)
	go func() {
		defer q.wg.Done()

		// Acquire semaphore slot (blocks if at capacity)
		select {
		case q.semaphore <- struct{}{}:
		case <-jobCtx.Done():
			result := JobResult{
				JobID:  jobID,
				Status: JobStatusCancelled,
				Error:  "cancelled while waiting for slot",
			}
			q.mu.Lock()
			entry.status = JobStatusCancelled
			entry.err = result.Error
			q.mu.Unlock()
			resultCh <- result
			q.resultCh <- result
			return
		}
		defer func() { <-q.semaphore }()

		// Mark as running
		startedAt := time.Now()
		q.mu.Lock()
		entry.status = JobStatusRunning
		entry.startedAt = startedAt
		q.mu.Unlock()

		if q.emitter != nil {
			q.emitter.Emit(Event{
				Type:      EventJobStarted,
				SessionID: req.SessionID,
				Timestamp: startedAt,
				Data: map[string]interface{}{
					"job_id": jobID,
				},
			})
		}

		// Execute
		resultStr, runErr := req.RunFunc(jobCtx)
		duration := time.Since(startedAt)

		var result JobResult
		if runErr != nil {
			result = JobResult{
				JobID:     jobID,
				Status:    JobStatusFailed,
				Error:     runErr.Error(),
				StartedAt: startedAt,
				Duration:  duration,
			}
			q.mu.Lock()
			entry.status = JobStatusFailed
			entry.err = runErr.Error()
			q.mu.Unlock()

			if q.emitter != nil {
				q.emitter.Emit(Event{
					Type:      EventJobFailed,
					SessionID: req.SessionID,
					Timestamp: time.Now(),
					Error:     runErr.Error(),
					Data: map[string]interface{}{
						"job_id":      jobID,
						"duration_ms": duration.Milliseconds(),
					},
				})
			}
		} else {
			result = JobResult{
				JobID:     jobID,
				Status:    JobStatusCompleted,
				Result:    resultStr,
				StartedAt: startedAt,
				Duration:  duration,
			}
			q.mu.Lock()
			entry.status = JobStatusCompleted
			entry.result = resultStr
			q.mu.Unlock()

			if q.emitter != nil {
				q.emitter.Emit(Event{
					Type:      EventJobCompleted,
					SessionID: req.SessionID,
					Timestamp: time.Now(),
					Data: map[string]interface{}{
						"job_id":      jobID,
						"duration_ms": duration.Milliseconds(),
						"result":      truncateString(resultStr, 500),
					},
				})
			}
		}

		resultCh <- result
		q.resultCh <- result
	}()

	return &JobHandle{
		JobID:    jobID,
		ResultCh: resultCh,
		Cancel:   cancel,
	}, nil
}

// Status returns the current status and result of a job.
func (q *LocalJobQueue) Status(_ context.Context, jobID string) (JobStatus, string, error) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	entry, ok := q.jobs[jobID]
	if !ok {
		return "", "", fmt.Errorf("job %s not found", jobID)
	}

	switch entry.status {
	case JobStatusCompleted:
		return entry.status, entry.result, nil
	case JobStatusFailed:
		return entry.status, entry.err, nil
	default:
		return entry.status, "", nil
	}
}

// Cancel cancels a running or pending job.
func (q *LocalJobQueue) Cancel(_ context.Context, jobID string) error {
	q.mu.RLock()
	entry, ok := q.jobs[jobID]
	q.mu.RUnlock()

	if !ok {
		return fmt.Errorf("job %s not found", jobID)
	}

	entry.cancel()
	return nil
}

// ResultCh returns the fan-in channel for completed job results.
func (q *LocalJobQueue) ResultCh() <-chan JobResult {
	return q.resultCh
}

// Shutdown cancels all jobs and waits for completion.
func (q *LocalJobQueue) Shutdown(ctx context.Context) error {
	q.mu.RLock()
	for _, entry := range q.jobs {
		entry.cancel()
	}
	q.mu.RUnlock()

	done := make(chan struct{})
	go func() {
		q.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Info("job_queue: all jobs completed during shutdown")
		return nil
	case <-ctx.Done():
		logger.Warn("job_queue: shutdown deadline exceeded")
		return ctx.Err()
	}
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
