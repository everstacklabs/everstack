package sandbox

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/google/uuid"
)

// CodeContext represents a persistent REPL session inside a sandbox.
type CodeContext struct {
	ID        string `json:"id"`
	Language  string `json:"language"`
	SessionID string `json:"session_id"`
	SandboxID string `json:"sandbox_id"`
	CreatedAt time.Time `json:"created_at"`

	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
	cancel context.CancelFunc
	alive  bool
	mu     sync.Mutex
}

// CodeEvent represents an output event from code execution.
type CodeEvent struct {
	Type string `json:"type"` // "stdout", "stderr", "exit", "error"
	Data string `json:"data"`
}

// CodeContextManager manages persistent REPL sessions across sandboxes.
type CodeContextManager struct {
	mu       sync.RWMutex
	contexts map[string]*CodeContext // keyed by context ID
	manager  *SandboxManager
}

// NewCodeContextManager creates a new CodeContextManager.
func NewCodeContextManager(manager *SandboxManager) *CodeContextManager {
	return &CodeContextManager{
		contexts: make(map[string]*CodeContext),
		manager:  manager,
	}
}

// replCommand returns the REPL command for a given language.
func replCommand(language string) ([]string, error) {
	switch strings.ToLower(language) {
	case "python", "python3":
		return []string{"python3", "-u", "-i"}, nil
	case "javascript", "node", "js":
		return []string{"node", "-i"}, nil
	case "bash", "sh":
		return []string{"bash", "--norc", "-i"}, nil
	default:
		return nil, fmt.Errorf("unsupported language: %s", language)
	}
}

// Create spawns a persistent REPL process inside the sandbox.
func (m *CodeContextManager) Create(ctx context.Context, sessionID, language string) (*CodeContext, error) {
	m.manager.mu.RLock()
	inst, ok := m.manager.instances[sessionID]
	m.manager.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no sandbox for session %s", sessionID)
	}

	cmd, err := replCommand(language)
	if err != nil {
		return nil, err
	}

	shellCtx, cancel := context.WithCancel(context.Background())
	shell, err := m.manager.backend.Shell(shellCtx, inst.ID, cmd)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start REPL: %w", err)
	}

	cc := &CodeContext{
		ID:        uuid.New().String(),
		Language:  language,
		SessionID: sessionID,
		SandboxID: inst.ID,
		CreatedAt: time.Now(),
		stdin:     shell.Conn,
		stdout:    io.NopCloser(shell.Conn),
		cancel:    cancel,
		alive:     true,
	}

	m.mu.Lock()
	m.contexts[cc.ID] = cc
	m.mu.Unlock()

	logger.WithFields("context_id", cc.ID, "language", language, "sandbox_id", inst.ID).
		Info("code_context: created")

	return cc, nil
}

// Execute writes code to the REPL stdin and streams output events.
func (m *CodeContextManager) Execute(ctx context.Context, sessionID, contextID, code string, onEvent func(CodeEvent)) error {
	m.mu.RLock()
	cc, ok := m.contexts[contextID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("code context not found: %s", contextID)
	}
	if cc.SessionID != sessionID {
		return fmt.Errorf("code context does not belong to session %s", sessionID)
	}

	cc.mu.Lock()
	if !cc.alive {
		cc.mu.Unlock()
		return fmt.Errorf("code context is no longer alive")
	}

	// Use a sentinel to detect when execution is complete.
	sentinel := fmt.Sprintf("__EVS_DONE_%d__", time.Now().UnixNano())
	var codeWithSentinel string
	switch strings.ToLower(cc.Language) {
	case "python", "python3":
		codeWithSentinel = code + "\nprint('" + sentinel + "')\n"
	case "javascript", "node", "js":
		codeWithSentinel = code + "\nconsole.log('" + sentinel + "')\n"
	default:
		codeWithSentinel = code + "\necho '" + sentinel + "'\n"
	}

	_, err := io.WriteString(cc.stdin, codeWithSentinel)
	cc.mu.Unlock()
	if err != nil {
		return fmt.Errorf("failed to write to REPL: %w", err)
	}

	// Read output until sentinel is found or context is cancelled.
	scanner := bufio.NewScanner(cc.stdout)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := scanner.Text()
		if strings.Contains(line, sentinel) {
			onEvent(CodeEvent{Type: "exit", Data: "0"})
			return nil
		}
		onEvent(CodeEvent{Type: "stdout", Data: line})
	}

	if err := scanner.Err(); err != nil {
		onEvent(CodeEvent{Type: "error", Data: err.Error()})
		return err
	}
	onEvent(CodeEvent{Type: "exit", Data: "0"})
	return nil
}

// Get returns a code context by ID.
func (m *CodeContextManager) Get(_ context.Context, contextID string) (*CodeContext, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cc, ok := m.contexts[contextID]
	if !ok {
		return nil, fmt.Errorf("code context not found: %s", contextID)
	}
	return cc, nil
}

// List returns all code contexts for a session, optionally filtered by language.
func (m *CodeContextManager) List(_ context.Context, sessionID, language string) ([]*CodeContext, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*CodeContext
	for _, cc := range m.contexts {
		if cc.SessionID != sessionID {
			continue
		}
		if language != "" && cc.Language != language {
			continue
		}
		out = append(out, cc)
	}
	return out, nil
}

// Delete removes and stops a code context.
func (m *CodeContextManager) Delete(_ context.Context, contextID string) error {
	m.mu.Lock()
	cc, ok := m.contexts[contextID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("code context not found: %s", contextID)
	}
	delete(m.contexts, contextID)
	m.mu.Unlock()

	cc.mu.Lock()
	cc.alive = false
	cc.mu.Unlock()
	if cc.cancel != nil {
		cc.cancel()
	}
	if cc.stdin != nil {
		cc.stdin.Close()
	}

	logger.WithFields("context_id", contextID).Info("code_context: deleted")
	return nil
}

// DeleteByLanguage removes all code contexts for a session/language combo.
func (m *CodeContextManager) DeleteByLanguage(_ context.Context, sessionID, language string) (int, error) {
	m.mu.Lock()
	var toDelete []*CodeContext
	for id, cc := range m.contexts {
		if cc.SessionID == sessionID && (language == "" || cc.Language == language) {
			toDelete = append(toDelete, cc)
			delete(m.contexts, id)
		}
	}
	m.mu.Unlock()

	for _, cc := range toDelete {
		cc.mu.Lock()
		cc.alive = false
		cc.mu.Unlock()
		if cc.cancel != nil {
			cc.cancel()
		}
		if cc.stdin != nil {
			cc.stdin.Close()
		}
	}

	return len(toDelete), nil
}
