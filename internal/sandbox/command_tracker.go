package sandbox

import (
	"container/ring"
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// CommandTracker tracks background/detached commands per sandbox.
type CommandTracker struct {
	mu       sync.RWMutex
	commands map[string]*TrackedCommand // keyed by cmd ID
}

// TrackedCommand represents a running or finished background command.
type TrackedCommand struct {
	ID         string     `json:"id"`
	SandboxID  string     `json:"sandbox_id"`
	Command    string     `json:"command"`
	CWD        string     `json:"cwd"`
	Running    bool       `json:"running"`
	ExitCode   *int       `json:"exit_code,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Cancel     context.CancelFunc `json:"-"`
	logBuf     *ring.Ring
	logMu      sync.Mutex
}

// NewCommandTracker creates a new CommandTracker.
func NewCommandTracker() *CommandTracker {
	return &CommandTracker{
		commands: make(map[string]*TrackedCommand),
	}
}

// Track registers a new background command and returns its ID.
func (t *CommandTracker) Track(sandboxID, command, cwd string, cancel context.CancelFunc) string {
	id := uuid.New().String()
	cmd := &TrackedCommand{
		ID:        id,
		SandboxID: sandboxID,
		Command:   command,
		CWD:       cwd,
		Running:   true,
		StartedAt: time.Now(),
		Cancel:    cancel,
		logBuf:    ring.New(1000), // keep last 1000 lines
	}
	t.mu.Lock()
	t.commands[id] = cmd
	t.mu.Unlock()
	return id
}

// AppendLog adds a log line to the command's ring buffer.
func (t *CommandTracker) AppendLog(cmdID, line string) {
	t.mu.RLock()
	cmd, ok := t.commands[cmdID]
	t.mu.RUnlock()
	if !ok {
		return
	}
	cmd.logMu.Lock()
	cmd.logBuf.Value = line
	cmd.logBuf = cmd.logBuf.Next()
	cmd.logMu.Unlock()
}

// Finish marks a command as completed with the given exit code.
func (t *CommandTracker) Finish(cmdID string, exitCode int) {
	t.mu.Lock()
	cmd, ok := t.commands[cmdID]
	t.mu.Unlock()
	if !ok {
		return
	}
	now := time.Now()
	cmd.Running = false
	cmd.ExitCode = &exitCode
	cmd.FinishedAt = &now
}

// Get returns a tracked command by ID.
func (t *CommandTracker) Get(cmdID string) (*TrackedCommand, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	cmd, ok := t.commands[cmdID]
	return cmd, ok
}

// GetLogs returns the buffered log lines for a command.
func (t *CommandTracker) GetLogs(cmdID string) ([]string, bool) {
	t.mu.RLock()
	cmd, ok := t.commands[cmdID]
	t.mu.RUnlock()
	if !ok {
		return nil, false
	}
	cmd.logMu.Lock()
	defer cmd.logMu.Unlock()

	var lines []string
	cmd.logBuf.Do(func(v interface{}) {
		if v != nil {
			lines = append(lines, v.(string))
		}
	})
	return lines, true
}

// Interrupt cancels a running command.
func (t *CommandTracker) Interrupt(cmdID string) bool {
	t.mu.RLock()
	cmd, ok := t.commands[cmdID]
	t.mu.RUnlock()
	if !ok || !cmd.Running {
		return false
	}
	if cmd.Cancel != nil {
		cmd.Cancel()
	}
	return true
}

// ListBySandbox returns all tracked commands for a sandbox.
func (t *CommandTracker) ListBySandbox(sandboxID string) []*TrackedCommand {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []*TrackedCommand
	for _, cmd := range t.commands {
		if cmd.SandboxID == sandboxID {
			out = append(out, cmd)
		}
	}
	return out
}
