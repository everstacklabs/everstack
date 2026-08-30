package egress

import (
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// AuditLogger is a buffered audit sink that batches egress events and
// delivers them to a callback. It follows the same buffered-channel pattern
// as SandboxManager's event writer.
type AuditLogger struct {
	ch     chan AuditEvent
	done   chan struct{}
	flush  func(events []AuditEvent)
	once   sync.Once
}

// AuditLoggerConfig configures the audit logger.
type AuditLoggerConfig struct {
	// BufferSize is the channel buffer size (default: 1024).
	BufferSize int

	// FlushInterval is how often to flush buffered events (default: 5s).
	FlushInterval time.Duration

	// FlushBatchSize is the max events per flush (default: 100).
	FlushBatchSize int

	// Flush is the callback to deliver batched events.
	// Typically writes to the sandbox_events table.
	Flush func(events []AuditEvent)
}

// NewAuditLogger creates and starts a buffered audit logger.
func NewAuditLogger(cfg AuditLoggerConfig) *AuditLogger {
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 1024
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 5 * time.Second
	}
	if cfg.FlushBatchSize <= 0 {
		cfg.FlushBatchSize = 100
	}

	al := &AuditLogger{
		ch:    make(chan AuditEvent, cfg.BufferSize),
		done:  make(chan struct{}),
		flush: cfg.Flush,
	}

	go al.run(cfg.FlushInterval, cfg.FlushBatchSize)
	return al
}

// Emit enqueues an audit event. Drops if buffer is full.
func (al *AuditLogger) Emit(event AuditEvent) {
	select {
	case al.ch <- event:
	default:
		logger.WithFields("sandbox_id", event.SandboxID, "domain", event.Domain).
			Debug("egress_audit: buffer full, dropping event")
	}
}

// Stop drains remaining events and shuts down.
func (al *AuditLogger) Stop() {
	al.once.Do(func() {
		close(al.ch)
		<-al.done
	})
}

func (al *AuditLogger) run(interval time.Duration, batchSize int) {
	defer close(al.done)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	batch := make([]AuditEvent, 0, batchSize)

	for {
		select {
		case event, ok := <-al.ch:
			if !ok {
				// Channel closed — flush remaining
				if len(batch) > 0 {
					al.flush(batch)
				}
				return
			}
			batch = append(batch, event)
			if len(batch) >= batchSize {
				al.flush(batch)
				batch = batch[:0]
			}

		case <-ticker.C:
			if len(batch) > 0 {
				al.flush(batch)
				batch = batch[:0]
			}
		}
	}
}
