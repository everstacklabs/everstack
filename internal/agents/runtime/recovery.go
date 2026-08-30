package runtime

import (
	"context"
	"database/sql"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// FailOrphanedSessions marks sessions that were running on this instance
// before a crash as failed and resets their persistent agents to idle.
// Since the goroutine state is lost, these sessions cannot be resumed;
// the client can re-issue RunTurn to start from the last checkpoint.
func FailOrphanedSessions(db *sqlx.DB, instanceID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Collect orphaned session IDs + agent IDs before updating, so we can
	// reset persistent agents and persist partial turn records.
	type orphanedSession struct {
		SessionID string         `db:"id"`
		AgentID   sql.NullString `db:"agent_id"`
		TurnCount int            `db:"turn_count"`
	}
	var orphaned []orphanedSession
	if err := db.SelectContext(ctx, &orphaned,
		`SELECT id, agent_id, turn_count FROM agent_sessions
		 WHERE instance_id = $1
		   AND status IN ('running', 'waiting_for_approval')`,
		instanceID); err != nil {
		logger.WithError(err).Warn("recovery: failed to query orphaned sessions")
	}

	res, err := db.ExecContext(ctx,
		`UPDATE agent_sessions
		 SET status = 'failed', completed_at = NOW()
		 WHERE instance_id = $1
		   AND status IN ('running', 'waiting_for_approval')`,
		instanceID)
	if err != nil {
		logger.WithError(err).Warn("recovery: failed to mark orphaned sessions")
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		logger.WithFields("failed_sessions", n, "instance_id", instanceID).
			Info("recovery: marked orphaned sessions as failed")
	}

	// Reset persistent agents that were running back to idle so they can
	// accept new turns after recovery.
	for _, s := range orphaned {
		if s.AgentID.Valid && s.AgentID.String != "" {
			if _, err := db.ExecContext(ctx,
				`UPDATE agent_definitions
				 SET lifecycle_status = 'idle', updated_at = NOW()
				 WHERE id = $1 AND lifecycle_status = 'running'`,
				s.AgentID.String); err != nil {
				logger.WithFields("agent_id", s.AgentID.String, "error", err.Error()).
					Warn("recovery: failed to reset persistent agent to idle")
			}
		}

		// Insert a partial turn record so the frontend can display the
		// interrupted turn instead of it vanishing completely.
		nextTurn := s.TurnCount + 1
		if _, err := db.ExecContext(ctx,
			`INSERT INTO agent_session_turns (session_id, turn_number, user_input, assistant_output, tool_calls, status, created_at)
			 VALUES ($1, $2, '', '[interrupted by server restart]', '[]', 'failed', NOW())
			 ON CONFLICT (session_id, turn_number) DO NOTHING`,
			s.SessionID, nextTurn); err != nil {
			logger.WithFields("session_id", s.SessionID, "turn_number", nextTurn, "error", err.Error()).
				Warn("recovery: failed to persist partial turn record")
		}
	}
}
