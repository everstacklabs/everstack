package observations

import (
	"context"
	"sync"
	"time"
)

// WorkflowTracker tracks workflow execution state and automatically manages step numbering
type WorkflowTracker struct {
	recorder *EnhancedRecorder

	mu        sync.RWMutex
	workflows map[string]*WorkflowState
}

// WorkflowState represents the current state of a workflow execution
type WorkflowState struct {
	WorkflowID   string
	TraceID      string
	WorkflowType string
	WorkflowName string
	StartTime    time.Time
	CurrentStep  uint32
	Observations []string // observation IDs
	Context      map[string]string
	mu           sync.Mutex
}

// NewWorkflowTracker creates a new workflow tracker
func NewWorkflowTracker(recorder *EnhancedRecorder) *WorkflowTracker {
	return &WorkflowTracker{
		recorder:  recorder,
		workflows: make(map[string]*WorkflowState),
	}
}

// StartWorkflow begins tracking a new workflow
func (t *WorkflowTracker) StartWorkflow(ctx context.Context, workflowID, traceID, workflowType, workflowName string, context map[string]string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	state := &WorkflowState{
		WorkflowID:   workflowID,
		TraceID:      traceID,
		WorkflowType: workflowType,
		WorkflowName: workflowName,
		StartTime:    time.Now(),
		CurrentStep:  0,
		Observations: []string{},
		Context:      context,
	}

	t.workflows[workflowID] = state

	// Record workflow metadata
	workflowContext := &WorkflowContext{
		WorkflowID:   workflowID,
		WorkflowType: workflowType,
		WorkflowName: workflowName,
		Context:      context,
	}

	obs := &ObservationRecord{
		ObservationID: workflowID + "-start",
		TraceID:       traceID,
		Name:          "workflow_start",
		StartTime:     state.StartTime,
		StatusCode:    "OK",
		Workflow:      workflowContext,
	}

	return t.recorder.RecordObservation(ctx, obs)
}

// RecordStep records a step in the workflow with automatic step numbering
func (t *WorkflowTracker) RecordStep(ctx context.Context, workflowID string, obs *ObservationRecord) error {
	t.mu.RLock()
	state, exists := t.workflows[workflowID]
	t.mu.RUnlock()

	if !exists {
		// If workflow not tracked, just record the observation without step tracking
		return t.recorder.RecordObservation(ctx, obs)
	}

	state.mu.Lock()
	state.CurrentStep++
	stepNum := state.CurrentStep
	state.Observations = append(state.Observations, obs.ObservationID)
	state.mu.Unlock()

	// Set the step number
	obs.Step = &stepNum

	// Set workflow context if not already set
	if obs.Workflow == nil {
		obs.Workflow = &WorkflowContext{
			WorkflowID:   state.WorkflowID,
			WorkflowType: state.WorkflowType,
			WorkflowName: state.WorkflowName,
			Context:      state.Context,
		}
	}

	return t.recorder.RecordObservation(ctx, obs)
}

// EndWorkflow marks a workflow as complete and cleans up tracking state
func (t *WorkflowTracker) EndWorkflow(ctx context.Context, workflowID string, success bool) error {
	t.mu.Lock()
	state, exists := t.workflows[workflowID]
	if !exists {
		t.mu.Unlock()
		return nil
	}
	delete(t.workflows, workflowID)
	t.mu.Unlock()

	// Record workflow end observation
	endTime := time.Now()
	duration := endTime.Sub(state.StartTime).Nanoseconds()
	statusCode := "OK"
	if !success {
		statusCode = "ERROR"
	}

	obs := &ObservationRecord{
		ObservationID: workflowID + "-end",
		TraceID:       state.TraceID,
		Name:          "workflow_end",
		StartTime:     state.StartTime,
		EndTime:       &endTime,
		Duration:      duration,
		StatusCode:    statusCode,
		Workflow: &WorkflowContext{
			WorkflowID:   state.WorkflowID,
			WorkflowType: state.WorkflowType,
			WorkflowName: state.WorkflowName,
			Context:      state.Context,
		},
	}

	return t.recorder.RecordObservation(ctx, obs)
}

// GetWorkflowState returns the current state of a workflow
func (t *WorkflowTracker) GetWorkflowState(workflowID string) (*WorkflowState, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	state, exists := t.workflows[workflowID]
	return state, exists
}

// GetCurrentStep returns the current step number for a workflow
func (t *WorkflowTracker) GetCurrentStep(workflowID string) uint32 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if state, exists := t.workflows[workflowID]; exists {
		state.mu.Lock()
		defer state.mu.Unlock()
		return state.CurrentStep
	}

	return 0
}

// UpdateWorkflowContext updates the context for a workflow
func (t *WorkflowTracker) UpdateWorkflowContext(workflowID string, updates map[string]string) {
	t.mu.RLock()
	state, exists := t.workflows[workflowID]
	t.mu.RUnlock()

	if !exists {
		return
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	for k, v := range updates {
		state.Context[k] = v
	}
}
