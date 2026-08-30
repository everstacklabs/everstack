package runtime

import (
	"context"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// DefaultSessionIdleMap defines per-plan-tier idle timeouts before a session
// is automatically hibernated. A value of 0 means no auto-hibernate.
var DefaultSessionIdleMap = map[string]time.Duration{
	"free":       2 * time.Minute,
	"basic":      5 * time.Minute,
	"pro":        10 * time.Minute,
	"enterprise": 0, // no auto-hibernate
}

// resolveSessionIdleTimeout returns the idle duration after which sessions
// should be hibernated. Uses the planTierResolver if set, otherwise defaults
// to the "free" tier timeout.
func (m *SessionManager) resolveSessionIdleTimeout() time.Duration {
	if m.planTierResolver != nil {
		// The reaper runs a single global query, so use the most conservative
		// (shortest) timeout. Per-tenant resolution would require a JOIN.
		tier := m.planTierResolver("")
		if d, ok := DefaultSessionIdleMap[tier]; ok {
			return d
		}
	}
	return DefaultSessionIdleMap["free"]
}

// reapIdleSessions hibernates sessions that have been idle (waiting_for_input)
// longer than the configured timeout. Called by the reaper goroutine.
func (m *SessionManager) reapIdleSessions() {
	if m.db == nil {
		return
	}
	timeout := m.resolveSessionIdleTimeout()
	if timeout == 0 {
		return // 0 = no auto-hibernate
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Atomically hibernate sessions idle longer than timeout.
	// CAS: only transitions waiting_for_input → hibernated.
	result, err := m.db.ExecContext(ctx, `
		UPDATE agent_sessions
		SET status = 'hibernated', hibernated_at = NOW()
		WHERE status = 'waiting_for_input'
		  AND hibernated_at IS NULL
		  AND updated_at < NOW() - $1::interval`,
		timeout.String())
	if err != nil {
		logger.WithError(err).Warn("session_manager: reapIdleSessions query failed")
		return
	}
	if n, _ := result.RowsAffected(); n > 0 {
		logger.WithFields("count", n).Info("session_manager: hibernated idle sessions")
	}
}
