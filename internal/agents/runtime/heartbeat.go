package runtime

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

const (
	heartbeatInterval    = 10 * time.Second
	staleCheckInterval   = 30 * time.Second
	staleThresholdClause = "interval '45 seconds'"
)

// HeartbeatWriter periodically updates heartbeat_at for all sessions owned
// by this instance, proving the instance is alive.
type HeartbeatWriter struct {
	db         *sqlx.DB
	instanceID string
	cancel     context.CancelFunc
	done       chan struct{}
}

// NewHeartbeatWriter creates and starts a heartbeat writer goroutine.
func NewHeartbeatWriter(db *sqlx.DB, instanceID string) *HeartbeatWriter {
	ctx, cancel := context.WithCancel(context.Background())
	hw := &HeartbeatWriter{
		db:         db,
		instanceID: instanceID,
		cancel:     cancel,
		done:       make(chan struct{}),
	}
	go hw.run(ctx)
	return hw
}

func (hw *HeartbeatWriter) run(ctx context.Context) {
	defer close(hw.done)
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	// Write an initial heartbeat immediately so sessions are marked alive on startup.
	hw.tick()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hw.tick()
		}
	}
}

func (hw *HeartbeatWriter) tick() {
	// Short timeout — heartbeat is non-critical and should not hold pool connections
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := hw.db.ExecContext(ctx,
		`UPDATE agent_sessions
		 SET heartbeat_at = NOW()
		 WHERE instance_id = $1
		   AND status IN ('running', 'waiting_for_approval')`,
		hw.instanceID)
	if err != nil {
		logger.WithFields("instance_id", hw.instanceID, "error", err.Error()).
			Warn("heartbeat_writer: failed to update heartbeat (pool may be exhausted)")
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		logger.WithFields("sessions", n).Debug("heartbeat_writer: heartbeat updated")
	}
}

// Stop terminates the heartbeat writer and waits for it to finish.
func (hw *HeartbeatWriter) Stop() {
	hw.cancel()
	<-hw.done
}

// StaleDetector periodically finds sessions whose owner instance has stopped
// heartbeating and marks them as failed.
type StaleDetector struct {
	db         *sqlx.DB
	instanceID string
	cancel     context.CancelFunc
	done       chan struct{}
}

// NewStaleDetector creates and starts a stale detector goroutine.
func NewStaleDetector(db *sqlx.DB, instanceID string) *StaleDetector {
	ctx, cancel := context.WithCancel(context.Background())
	sd := &StaleDetector{
		db:         db,
		instanceID: instanceID,
		cancel:     cancel,
		done:       make(chan struct{}),
	}
	go sd.run(ctx)
	return sd
}

func (sd *StaleDetector) run(ctx context.Context) {
	defer close(sd.done)
	ticker := time.NewTicker(staleCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sd.tick()
		}
	}
}

func (sd *StaleDetector) tick() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Only fail sessions owned by OTHER instances to avoid self-reaping
	// during GC pauses or temporary slowdowns.
	res, err := sd.db.ExecContext(ctx,
		`UPDATE agent_sessions
		 SET status = 'failed', completed_at = NOW()
		 WHERE heartbeat_at < NOW() - `+staleThresholdClause+`
		   AND status IN ('running', 'waiting_for_approval')
		   AND instance_id IS NOT NULL
		   AND instance_id != $1`,
		sd.instanceID)
	if err != nil {
		logger.WithError(err).Warn("stale_detector: query failed")
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		logger.WithFields("failed_sessions", n).Info("stale_detector: marked stale sessions as failed")
	}
}

// Stop terminates the stale detector and waits for it to finish.
func (sd *StaleDetector) Stop() {
	sd.cancel()
	<-sd.done
}
