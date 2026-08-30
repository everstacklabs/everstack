package sandbox

import (
	"context"
	"encoding/json"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// EventType constants for sandbox lifecycle events.
type EventType string

const (
	EventCreated        EventType = "created"
	EventImagePull      EventType = "image.pull"
	EventContainerStart EventType = "container.start"
	EventReady          EventType = "ready"
	EventExecStart      EventType = "exec.start"
	EventExecDone       EventType = "exec.done"
	EventFileWrite      EventType = "file.write"
	EventFileRead       EventType = "file.read"
	EventShellOpen      EventType = "shell.open"
	EventShellClose     EventType = "shell.close"
	EventPortExpose     EventType = "port.expose"
	EventPortUnexpose   EventType = "port.unexpose"
	EventCronTrigger    EventType = "cron.trigger"
	EventWebhookTrigger EventType = "webhook.trigger"
	EventIdleWarning    EventType = "idle.warning"
	EventDestroyStart   EventType = "destroy.start"
	EventDestroyed      EventType = "destroyed"
	EventUsageMetered   EventType = "usage.metered"
	EventError          EventType = "error"

	// Git clone events
	EventGitCloneDeferred EventType = "git.clone.deferred"
	EventGitCloneStart    EventType = "git.clone.start"
	EventGitClone         EventType = "git.clone"
	EventGitCloneFail     EventType = "git.clone.fail"

	// Lifecycle events
	EventSandboxStopped    EventType = "sandbox.stopped"
	EventSandboxRevived    EventType = "sandbox.revived"
	EventSandboxTerminated EventType = "sandbox.terminated"
	EventSandboxArchived   EventType = "sandbox.archived"
	EventSandboxError      EventType = "sandbox.error"

	// SSH events
	EventSSHReady   EventType = "ssh.ready"
	EventSSHConnect EventType = "ssh.connect"

	// Filesystem events
	EventFileDelete EventType = "file.delete"
	EventFileMove   EventType = "file.move"
	EventFileChmod  EventType = "file.chmod"
	EventDirCreate  EventType = "dir.create"
	EventDirDelete  EventType = "dir.delete"

	// Egress events
	EventEgressAllowed EventType = "egress.allowed"
	EventEgressBlocked EventType = "egress.blocked"
)

// SandboxEvent represents a single lifecycle event for a sandbox instance.
type SandboxEvent struct {
	ID         int64           `json:"id" db:"id"`
	SandboxID  string          `json:"sandbox_id" db:"sandbox_id"`
	SessionID  string          `json:"session_id" db:"session_id"`
	TenantID   string          `json:"tenant_id" db:"tenant_id"`
	EventType  EventType       `json:"event_type" db:"event_type"`
	Message    string          `json:"message" db:"message"`
	Metadata   json.RawMessage `json:"metadata" db:"metadata"`
	DurationMs *int64          `json:"duration_ms,omitempty" db:"duration_ms"`
	Error      string          `json:"error,omitempty" db:"error"`
	CreatedAt  time.Time       `json:"created_at" db:"created_at"`
}

// eventEnvelope wraps all recordEvent parameters for buffered delivery.
type eventEnvelope struct {
	sandboxID  string
	sessionID  string
	tenantID   string
	eventType  EventType
	message    string
	metadata   map[string]interface{}
	durationMs *int64
	errStr     string
}

// startEventWriter launches a single goroutine that reads from eventCh and
// writes to the DB. Follows the same context-cancel + done-channel pattern as
// the Reaper.
func (m *SandboxManager) startEventWriter() {
	ctx, cancel := context.WithCancel(context.Background())
	m.eventCancel = cancel
	m.eventDone = make(chan struct{})
	go m.runEventWriter(ctx)
}

func (m *SandboxManager) runEventWriter(ctx context.Context) {
	defer close(m.eventDone)
	for {
		select {
		case env, ok := <-m.eventCh:
			if !ok {
				return
			}
			m.writeEvent(env)
		case <-ctx.Done():
			// Drain remaining events before exiting.
			for {
				select {
				case env, ok := <-m.eventCh:
					if !ok {
						return
					}
					m.writeEvent(env)
				default:
					return
				}
			}
		}
	}
}

// writeEvent performs the actual DB insert for a single event envelope.
func (m *SandboxManager) writeEvent(env eventEnvelope) {
	if m.db == nil {
		return
	}

	var metadataJSON []byte
	if env.metadata != nil {
		metadataJSON, _ = json.Marshal(env.metadata)
	}
	if metadataJSON == nil {
		metadataJSON = []byte("{}")
	}

	const q = `
		INSERT INTO sandbox_events
			(sandbox_id, session_id, tenant_id, event_type, message, metadata, duration_ms, error)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''))`

	writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := m.db.ExecContext(writeCtx, q,
		env.sandboxID, env.sessionID, env.tenantID,
		string(env.eventType), env.message, metadataJSON,
		env.durationMs, env.errStr,
	); err != nil {
		logger.WithFields("sandbox_id", env.sandboxID, "event_type", string(env.eventType), "error", err.Error()).
			Debug("sandbox_manager: failed to record event")
	}
}

// stopEventWriter cancels the writer context and waits for the goroutine to
// drain and exit.
func (m *SandboxManager) stopEventWriter() {
	if m.eventCancel != nil {
		m.eventCancel()
	}
	if m.eventDone != nil {
		<-m.eventDone
	}
}

// recordEvent enqueues a lifecycle event for async DB insertion.
// Fire-and-forget: if the buffer is full the event is dropped and logged.
func (m *SandboxManager) recordEvent(sandboxID, sessionID, tenantID string, eventType EventType, message string, metadata map[string]interface{}, durationMs *int64, errStr string) {
	env := eventEnvelope{
		sandboxID:  sandboxID,
		sessionID:  sessionID,
		tenantID:   tenantID,
		eventType:  eventType,
		message:    message,
		metadata:   metadata,
		durationMs: durationMs,
		errStr:     errStr,
	}
	select {
	case m.eventCh <- env:
	default:
		logger.WithFields("sandbox_id", sandboxID, "event_type", string(eventType)).
			Debug("sandbox_manager: event buffer full, dropping event")
	}
}

// RecordEvent is the exported wrapper for recording sandbox events.
// Used by subsystems (proxy, scheduler, webhook router) that hold a manager reference.
func (m *SandboxManager) RecordEvent(sandboxID, sessionID, tenantID string, eventType EventType, message string, metadata map[string]interface{}, durationMs *int64, errStr string) {
	m.recordEvent(sandboxID, sessionID, tenantID, eventType, message, metadata, durationMs, errStr)
}
