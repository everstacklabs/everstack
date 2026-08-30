package sandbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/robfig/cron/v3"
)

// CronNotifier is called when a cron job with channel notification fires.
// Implemented by the channel manager to send messages back to messaging platforms.
type CronNotifier interface {
	SendCronNotification(ctx context.Context, channelConfigID, channelRef, threadRef, message string) error
}

// CronScheduler periodically checks for due cron jobs and executes them.
// Follows the Reaper pattern: cancel func + done channel + ticker.
type CronScheduler struct {
	manager  *SandboxManager
	interval time.Duration
	parser   cron.Parser
	cancel   context.CancelFunc
	done     chan struct{}
	notifier CronNotifier // optional: sends channel notifications when crons fire
}

// SandboxCron represents a cron schedule for automated sandbox execution.
type SandboxCron struct {
	ID             int64           `json:"id" db:"id"`
	TenantID       string          `json:"tenant_id" db:"tenant_id"`
	SandboxID      string          `json:"sandbox_id" db:"sandbox_id"`
	SessionID      string          `json:"session_id" db:"session_id"`
	Name           string          `json:"name" db:"name"`
	Schedule       string          `json:"schedule" db:"schedule"`
	Command        string          `json:"command" db:"command"`
	WorkDir        string          `json:"work_dir" db:"work_dir"`
	TimeoutSeconds int             `json:"timeout_seconds" db:"timeout_seconds"`
	Enabled        bool            `json:"enabled" db:"enabled"`
	LastRunAt      *time.Time      `json:"last_run_at,omitempty" db:"last_run_at"`
	NextRunAt      *time.Time      `json:"next_run_at,omitempty" db:"next_run_at"`
	RunCount       int             `json:"run_count" db:"run_count"`
	ErrorCount     int             `json:"error_count" db:"error_count"`
	LastError      sql.NullString  `json:"last_error,omitempty" db:"last_error"`
	AutoRecreate   bool            `json:"auto_recreate" db:"auto_recreate"`
	SandboxConfig  json.RawMessage `json:"sandbox_config" db:"sandbox_config"`
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at" db:"updated_at"`

	// Channel notification — when set, the scheduler sends a message to
	// the originating channel when the cron fires.
	ChannelConfigID sql.NullString `json:"channel_config_id,omitempty" db:"channel_config_id"`
	ChannelRef      sql.NullString `json:"channel_ref,omitempty" db:"channel_ref"`
	ThreadRef       sql.NullString `json:"thread_ref,omitempty" db:"thread_ref"`
	NotifyMessage   sql.NullString `json:"notify_message,omitempty" db:"notify_message"`
}

// NewCronScheduler creates and starts a new cron scheduler.
func NewCronScheduler(manager *SandboxManager, interval time.Duration) *CronScheduler {
	ctx, cancel := context.WithCancel(context.Background())
	s := &CronScheduler{
		manager:  manager,
		interval: interval,
		parser:   cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	go s.run(ctx)
	return s
}

func (s *CronScheduler) run(ctx context.Context) {
	defer close(s.done)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

func (s *CronScheduler) tick() {
	db := s.manager.DB()
	if db == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Query due crons using advisory lock to prevent double execution
	var crons []SandboxCron
	const q = `
		SELECT * FROM sandbox_crons
		WHERE enabled = true AND next_run_at <= NOW()
		ORDER BY next_run_at ASC
		LIMIT 20`

	if err := db.SelectContext(ctx, &crons, q); err != nil {
		logger.WithFields("error", err.Error()).Debug("cron_scheduler: failed to query due crons")
		return
	}

	for _, c := range crons {
		s.executeCron(ctx, c)
	}
}

func (s *CronScheduler) executeCron(ctx context.Context, c SandboxCron) {
	db := s.manager.DB()
	start := time.Now()

	// Try advisory lock to prevent double execution
	var locked bool
	lockKey := fmt.Sprintf("cron_%d", c.ID)
	_ = db.GetContext(ctx, &locked, `SELECT pg_try_advisory_lock(hashtext($1))`, lockKey)
	if !locked {
		return
	}
	defer db.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, lockKey)

	logger.WithFields("cron_id", c.ID, "name", c.Name, "schedule", c.Schedule).
		Info("cron_scheduler: executing cron job")

	// Get or recreate sandbox
	var inst *Instance
	var err error
	if c.AutoRecreate && len(c.SandboxConfig) > 0 {
		inst, err = s.manager.GetOrRecreate(ctx, c.SessionID, c.TenantID, c.SandboxConfig)
	} else {
		inst, _ = s.manager.GetInstance(c.SessionID)
		if inst == nil {
			err = fmt.Errorf("sandbox not running for session %s", c.SessionID)
		}
	}

	var execErr string
	var execOutput string
	var durationMs int64
	if err != nil {
		execErr = err.Error()
	} else {
		result, execError := s.execCronCommand(ctx, c)
		durationMs = time.Since(start).Milliseconds()
		if execError != nil {
			execErr = execError.Error()
		} else if result == nil {
			execErr = "cron execution returned no result"
		} else if result.ExitCode != 0 {
			execErr = formatCronExitError(result)
		}
		if result != nil {
			execOutput = strings.TrimSpace(strings.TrimSpace(result.Stdout) + "\n" + strings.TrimSpace(result.Stderr))
		}
	}

	// Send channel notification if configured
	if s.notifier != nil && c.ChannelConfigID.Valid && c.ChannelRef.Valid {
		notifyMsg := c.NotifyMessage.String
		if notifyMsg == "" {
			// Use command output as the notification, or fall back to cron name
			notifyMsg = execOutput
			if notifyMsg == "" {
				notifyMsg = fmt.Sprintf("Cron job \"%s\" completed.", c.Name)
			}
		}
		if execErr != "" {
			notifyMsg = fmt.Sprintf("Cron job \"%s\" failed: %s", c.Name, execErr)
		}
		if notifyErr := s.notifier.SendCronNotification(ctx, c.ChannelConfigID.String, c.ChannelRef.String, c.ThreadRef.String, notifyMsg); notifyErr != nil {
			logger.WithFields("cron_id", c.ID, "error", notifyErr.Error()).
				Warn("cron_scheduler: failed to send channel notification")
		}
	}

	// Reset keep-warm clock to end-of-execution.
	s.manager.touchLastUsed(c.SessionID)

	// Record trigger
	const triggerQ = `
		INSERT INTO sandbox_triggers
			(trigger_type, trigger_id, sandbox_id, status, error, duration_ms)
		VALUES ('cron', $1, $2, $3, NULLIF($4, ''), $5)`
	status := "completed"
	if execErr != "" {
		status = "failed"
	}
	db.ExecContext(ctx, triggerQ, c.ID, c.SandboxID, status, execErr, durationMs)

	// Record sandbox event
	if inst != nil {
		go s.manager.recordEvent(c.SandboxID, c.SessionID, c.TenantID, EventCronTrigger, fmt.Sprintf("Cron '%s' executed", c.Name), map[string]interface{}{
			"cron_id":  c.ID,
			"schedule": c.Schedule,
			"command":  c.Command,
			"status":   status,
		}, &durationMs, execErr)
	}

	// Update cron: next_run_at, run_count, error_count
	nextRun := s.calculateNextRun(c.Schedule)
	updateQ := `
		UPDATE sandbox_crons SET
			last_run_at = NOW(),
			next_run_at = $1,
			run_count = run_count + 1,
			error_count = CASE WHEN $2 = '' THEN error_count ELSE error_count + 1 END,
			last_error = NULLIF($2, ''),
			updated_at = NOW()
		WHERE id = $3`
	db.ExecContext(ctx, updateQ, nextRun, execErr, c.ID)
}

func (s *CronScheduler) execCronCommand(ctx context.Context, c SandboxCron) (*ExecResult, error) {
	baseReq := ExecRequest{
		Command: cronCommandArgs(c.Command),
		WorkDir: c.WorkDir,
		Timeout: time.Duration(c.TimeoutSeconds) * time.Second,
	}
	result, err := s.manager.Exec(ctx, c.SessionID, baseReq)
	if err != nil {
		return result, err
	}
	if result.ExitCode != 126 {
		return result, nil
	}

	fallbackCmd := cronInterpreterFallback(c.Command)
	if len(fallbackCmd) == 0 {
		return result, nil
	}

	logger.WithFields("cron_name", c.Name, "command", c.Command, "fallback", strings.Join(fallbackCmd, " ")).
		Info("cron_scheduler: retrying cron with interpreter fallback")

	fallbackReq := ExecRequest{
		Command: fallbackCmd,
		WorkDir: c.WorkDir,
		Timeout: time.Duration(c.TimeoutSeconds) * time.Second,
	}
	return s.manager.Exec(ctx, c.SessionID, fallbackReq)
}

func cronCommandArgs(command string) []string {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return []string{"sh", "-c", command}
	}
	// Always wrap cron commands in a shell — they are inherently shell
	// commands and may contain env var prefixes (TZ=...), pipes, redirects,
	// or other constructs that require shell interpretation.
	return []string{"sh", "-c", trimmed}
}

func cronInterpreterFallback(command string) []string {
	parts := strings.Fields(strings.TrimSpace(command))
	if len(parts) != 1 {
		return nil
	}

	script := parts[0]
	switch strings.ToLower(filepath.Ext(script)) {
	case ".py":
		return []string{"python3", script}
	case ".sh":
		return []string{"sh", script}
	case ".js":
		return []string{"node", script}
	case ".ts":
		return []string{"bun", script}
	default:
		return nil
	}
}

func formatCronExitError(result *ExecResult) string {
	if result == nil {
		return "execution failed"
	}
	stderr := strings.TrimSpace(result.Stderr)
	stdout := strings.TrimSpace(result.Stdout)
	if stderr != "" {
		return fmt.Sprintf("exit code %d: %s", result.ExitCode, stderr)
	}
	if stdout != "" {
		return fmt.Sprintf("exit code %d: %s", result.ExitCode, stdout)
	}
	return fmt.Sprintf("exit code %d", result.ExitCode)
}

func (s *CronScheduler) calculateNextRun(schedule string) *time.Time {
	sched, err := s.parser.Parse(schedule)
	if err != nil {
		return nil
	}
	next := sched.Next(time.Now())
	return &next
}

// RunCronNow executes a persisted cron immediately.
func (m *SandboxManager) RunCronNow(ctx context.Context, cronID int64) error {
	if m == nil || m.DB() == nil {
		return fmt.Errorf("database not available")
	}

	var c SandboxCron
	if err := m.DB().GetContext(ctx, &c, `SELECT * FROM sandbox_crons WHERE id = $1`, cronID); err != nil {
		return fmt.Errorf("load cron %d: %w", cronID, err)
	}

	scheduler := &CronScheduler{
		manager: m,
		parser:  cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
	}
	scheduler.executeCron(ctx, c)
	return nil
}

// SetNotifier sets the channel notifier for cron notifications.
func (s *CronScheduler) SetNotifier(n CronNotifier) {
	s.notifier = n
}

// Stop shuts down the cron scheduler and waits for it to finish.
func (s *CronScheduler) Stop() {
	s.cancel()
	<-s.done
}
