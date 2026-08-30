package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// ForkConfig controls fork (context branch) behavior.
type ForkConfig struct {
	Enabled       bool          `json:"enabled"`
	MaxConcurrent int           `json:"max_concurrent"`
	MaxDepth      int           `json:"max_depth"` // fork-of-fork limit
	Timeout       time.Duration `json:"timeout"`
}

// DefaultForkConfig returns sensible defaults.
func DefaultForkConfig() ForkConfig {
	return ForkConfig{
		Enabled:       false,
		MaxConcurrent: 3,
		MaxDepth:      1,
		Timeout:       2 * time.Minute,
	}
}

// ParseForkConfig extracts fork configuration from agent config.
func ParseForkConfig(config map[string]interface{}) ForkConfig {
	cfg := DefaultForkConfig()
	if config == nil {
		return cfg
	}
	forkRaw, ok := config["fork"]
	if !ok {
		return cfg
	}
	forkMap, ok := forkRaw.(map[string]interface{})
	if !ok {
		return cfg
	}

	if enabled, ok := forkMap["enabled"].(bool); ok {
		cfg.Enabled = enabled
	}
	if maxConcurrent, ok := forkMap["max_concurrent"].(float64); ok && maxConcurrent >= 1 && maxConcurrent <= 20 {
		cfg.MaxConcurrent = int(maxConcurrent)
	}
	if maxDepth, ok := forkMap["max_depth"].(float64); ok && maxDepth >= 0 && maxDepth <= 5 {
		cfg.MaxDepth = int(maxDepth)
	}
	if timeout, ok := forkMap["timeout"].(float64); ok && timeout >= 10 && timeout <= 600 {
		cfg.Timeout = time.Duration(timeout) * time.Second
	}
	return cfg
}

// ForkResult is the outcome of a completed fork.
type ForkResult struct {
	ForkID      string
	Instruction string
	Conclusion  string
	Status      string // "completed", "failed", "cancelled"
	Error       string
	Duration    time.Duration
	Usage       UsageDelta
}

// forkEntry tracks a single active fork.
type forkEntry struct {
	id          string
	instruction string
	cancel      context.CancelFunc
	startedAt   time.Time
}

// ForkManager manages concurrent fork goroutines for a session.
type ForkManager struct {
	config       ForkConfig
	activeForks  map[string]*forkEntry
	mu           sync.Mutex
	resultCh     chan ForkResult // fan-in, read by parent loop
	engine       *Engine
	emitter      *Emitter
	currentDepth int
	BranchStore  *BranchStore
}

// NewForkManager creates a new fork manager.
func NewForkManager(config ForkConfig, engine *Engine, emitter *Emitter) *ForkManager {
	return &ForkManager{
		config:      config,
		activeForks: make(map[string]*forkEntry),
		resultCh:    make(chan ForkResult, 16),
		engine:      engine,
		emitter:     emitter,
	}
}

// SetEmitter sets the emitter after construction (for deferred wiring).
func (fm *ForkManager) SetEmitter(e *Emitter) { fm.emitter = e }

// ResultCh returns the channel that receives fork conclusions.
func (fm *ForkManager) ResultCh() <-chan ForkResult {
	return fm.resultCh
}

// Fork clones the current messages, appends the instruction, and runs a
// new Loop in a goroutine. Returns the fork ID immediately.
func (fm *ForkManager) Fork(
	ctx context.Context,
	instruction string,
	currentMessages []gw.Message,
	input *LoopInput,
) (string, error) {
	if !fm.config.Enabled {
		return "", fmt.Errorf("fork is disabled")
	}

	fm.mu.Lock()
	if len(fm.activeForks) >= fm.config.MaxConcurrent {
		fm.mu.Unlock()
		return "", fmt.Errorf("maximum concurrent forks reached (%d)", fm.config.MaxConcurrent)
	}

	forkID := uuid.New().String()
	forkCtx, cancel := context.WithTimeout(ctx, fm.config.Timeout)

	entry := &forkEntry{
		id:          forkID,
		instruction: instruction,
		cancel:      cancel,
		startedAt:   time.Now(),
	}
	fm.activeForks[forkID] = entry
	fm.mu.Unlock()

	// Clone messages
	clonedMessages := make([]gw.Message, len(currentMessages))
	copy(clonedMessages, currentMessages)

	// Append the fork instruction as a user message
	clonedMessages = append(clonedMessages, gw.Message{
		Role:    gw.RoleUser,
		Content: []gw.ContentPart{{Type: "text", Text: strPtr(instruction)}},
	})

	fm.emitter.Emit(Event{
		Type:      EventForkStart,
		SessionID: input.SessionID,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"fork_id":     forkID,
			"instruction": truncateString(instruction, 200),
		},
	})

	go fm.runFork(forkCtx, forkID, clonedMessages, input)

	return forkID, nil
}

// runFork executes a fork loop in a goroutine.
func (fm *ForkManager) runFork(
	ctx context.Context,
	forkID string,
	messages []gw.Message,
	parentInput *LoopInput,
) {
	defer func() {
		fm.mu.Lock()
		if entry, ok := fm.activeForks[forkID]; ok {
			entry.cancel()
			delete(fm.activeForks, forkID)
		}
		fm.mu.Unlock()
	}()

	startedAt := time.Now()

	// Create a fork-specific emitter that forwards to the parent
	forkEmitter := NewEmitter()
	defer forkEmitter.Close()
	forkEmitter.AddSink(EventSinkFunc(func(e Event) error {
		e.Data = ensureForkData(e.Data)
		e.Data["fork_id"] = forkID
		fm.emitter.Emit(e)
		return nil
	}))

	// Fork uses a simpler config: no tools, just thinking
	forkConfig := LoopConfig{
		MaxIterations:      10,
		MaxToolCallsPerTurn: 0,
		MaxHistoryMessages: 100,
		EnableStreaming:    false,
		TurnTimeout:        fm.config.Timeout,
	}

	forkLoop := NewLoop(fm.engine, nil, forkEmitter, forkConfig)

	// Fork input: same model but no tools, no synthetic tools
	forkInput := &LoopInput{
		TenantID:     parentInput.TenantID,
		AgentID:      parentInput.AgentID,
		SessionID:    fmt.Sprintf("fork_%s", forkID),
		Model:        parentInput.Model,
		SystemPrompt: "", // Messages already include system prompt from parent
		Sampling:     parentInput.Sampling,
		UserInput:    "", // Already appended to cloned messages
	}

	// Build state from cloned messages (skip the user input since it's already there)
	// The fork instruction is the "user input" for this loop
	if len(messages) > 0 {
		lastMsg := messages[len(messages)-1]
		if len(lastMsg.Content) > 0 && lastMsg.Content[0].Text != nil {
			forkInput.UserInput = *lastMsg.Content[0].Text
		}
	}
	state := &LoopState{
		Messages: messages[:len(messages)-1],
	}

	finalState, err := forkLoop.Run(ctx, state, forkInput)
	duration := time.Since(startedAt)

	var result ForkResult
	if err != nil {
		result = ForkResult{
			ForkID:      forkID,
			Instruction: truncateString(fm.activeForks[forkID].instruction, 200),
			Status:      "failed",
			Error:       err.Error(),
			Duration:    duration,
		}
		fm.emitter.Emit(Event{
			Type:      EventForkFailed,
			SessionID: parentInput.SessionID,
			Timestamp: time.Now(),
			Error:     err.Error(),
			Data: map[string]interface{}{
				"fork_id":     forkID,
				"duration_ms": duration.Milliseconds(),
			},
		})
	} else if ctx.Err() != nil {
		result = ForkResult{
			ForkID:      forkID,
			Instruction: truncateString(fm.activeForks[forkID].instruction, 200),
			Status:      "cancelled",
			Error:       "fork timed out or was cancelled",
			Duration:    duration,
		}
		fm.emitter.Emit(Event{
			Type:      EventForkCancelled,
			SessionID: parentInput.SessionID,
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"fork_id":     forkID,
				"duration_ms": duration.Milliseconds(),
			},
		})
	} else {
		result = ForkResult{
			ForkID:      forkID,
			Instruction: truncateString(parentInput.UserInput, 200),
			Conclusion:  finalState.LastAssistantText,
			Status:      "completed",
			Duration:    duration,
			Usage: UsageDelta{
				PromptTokens:     finalState.CumulativeUsage.PromptTokens,
				CompletionTokens: finalState.CumulativeUsage.CompletionTokens,
				TotalTokens:      finalState.CumulativeUsage.TotalTokens,
			},
		}
		fm.emitter.Emit(Event{
			Type:      EventForkCompleted,
			SessionID: parentInput.SessionID,
			Timestamp: time.Now(),
			Usage: &UsageDelta{
				PromptTokens:     finalState.CumulativeUsage.PromptTokens,
				CompletionTokens: finalState.CumulativeUsage.CompletionTokens,
				TotalTokens:      finalState.CumulativeUsage.TotalTokens,
			},
			Data: map[string]interface{}{
				"fork_id":     forkID,
				"duration_ms": duration.Milliseconds(),
				"conclusion":  truncateString(finalState.LastAssistantText, 500),
			},
		})
	}

	// Persist branch trace
	if fm.BranchStore != nil && finalState != nil {
		var msgBytes json.RawMessage
		if b, merr := json.Marshal(finalState.Messages); merr == nil {
			msgBytes = b
		}
		branchStatus := result.Status
		fm.BranchStore.SaveBranch(ctx, &BranchRecord{
			ID:               forkID,
			SessionID:        parentInput.SessionID,
			TenantID:         parentInput.TenantID,
			AgentID:          parentInput.AgentID,
			Source:           "fork",
			Instruction:      truncateString(result.Instruction, 2000),
			Conclusion:       truncateString(result.Conclusion, 2000),
			Status:           branchStatus,
			Messages:         msgBytes,
			PromptTokens:     int(result.Usage.PromptTokens),
			CompletionTokens: int(result.Usage.CompletionTokens),
			TotalTokens:      int(result.Usage.TotalTokens),
			DurationMs:       duration.Milliseconds(),
		})
	}

	fm.resultCh <- result
}

// CancelAll cancels all active forks.
func (fm *ForkManager) CancelAll() {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	for _, entry := range fm.activeForks {
		entry.cancel()
	}
}

// ActiveCount returns the number of active forks.
func (fm *ForkManager) ActiveCount() int {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	return len(fm.activeForks)
}

func ensureForkData(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return make(map[string]interface{})
	}
	return m
}
