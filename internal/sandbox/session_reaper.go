package sandbox

import (
	"context"
	"os"
	"strconv"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// ShellSessionReaperConfig governs the idle-session cleanup loop.
// All knobs read from environment variables at construction time
// (see NewShellSessionReaperFromEnv); callers can also build the
// struct directly for tests.
type ShellSessionReaperConfig struct {
	// IdleTTL is how long a session can sit with zero attached
	// clients AND no activity before it gets killed. Default 24h.
	IdleTTL time.Duration
	// Interval is the gap between sweeps. Default 1h. Lowering this
	// trades a small increase in vsock chatter for faster reclamation.
	Interval time.Duration
	// Enabled toggles the whole loop. Default true. Operators can
	// turn it off via EVERSTACK_SHELL_SESSION_REAPER_ENABLED=false
	// during an incident or while debugging a stuck session.
	Enabled bool
}

// DefaultShellSessionReaperConfig returns the production defaults.
// Exported so tests and config-validation code can reference them.
func DefaultShellSessionReaperConfig() ShellSessionReaperConfig {
	return ShellSessionReaperConfig{
		IdleTTL:  24 * time.Hour,
		Interval: 1 * time.Hour,
		Enabled:  true,
	}
}

// NewShellSessionReaperFromEnv returns a config populated from
// environment variables, falling through to defaults when unset.
//
//	EVERSTACK_SHELL_SESSION_IDLE_TTL          duration (e.g. "24h")
//	EVERSTACK_SHELL_SESSION_REAPER_INTERVAL   duration (e.g. "1h")
//	EVERSTACK_SHELL_SESSION_REAPER_ENABLED    "true"/"false"
//
// Invalid values are logged and the default substituted — we don't
// want a typo'd env var to silently leave the reaper disabled.
func NewShellSessionReaperFromEnv() ShellSessionReaperConfig {
	cfg := DefaultShellSessionReaperConfig()
	if v := os.Getenv("EVERSTACK_SHELL_SESSION_IDLE_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.IdleTTL = d
		} else {
			logger.WithFields("value", v, "default", cfg.IdleTTL.String()).
				Warn("session_reaper: invalid EVERSTACK_SHELL_SESSION_IDLE_TTL, using default")
		}
	}
	if v := os.Getenv("EVERSTACK_SHELL_SESSION_REAPER_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.Interval = d
		} else {
			logger.WithFields("value", v, "default", cfg.Interval.String()).
				Warn("session_reaper: invalid EVERSTACK_SHELL_SESSION_REAPER_INTERVAL, using default")
		}
	}
	if v := os.Getenv("EVERSTACK_SHELL_SESSION_REAPER_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Enabled = b
		} else {
			logger.WithFields("value", v).
				Warn("session_reaper: invalid EVERSTACK_SHELL_SESSION_REAPER_ENABLED, keeping enabled=true")
		}
	}
	return cfg
}

// ShellSessionReaper periodically kills persistent shell sessions
// that have been idle past the configured TTL. Runs on the gateway
// rather than fcagent because (a) the gateway already owns cross-
// fcagent visibility via the discovery layer, and (b) putting policy
// next to the rest of the per-tenant lifecycle logic keeps it
// reviewable in one place.
type ShellSessionReaper struct {
	manager *SandboxManager
	cfg     ShellSessionReaperConfig
}

// NewShellSessionReaper constructs a reaper bound to the given
// SandboxManager. Callers wire Run into a long-running goroutine
// during gateway startup.
func NewShellSessionReaper(mgr *SandboxManager, cfg ShellSessionReaperConfig) *ShellSessionReaper {
	return &ShellSessionReaper{manager: mgr, cfg: cfg}
}

// Run drives the periodic sweep until ctx is cancelled. Blocks. The
// first sweep happens one Interval after Run is invoked so the
// gateway doesn't immediately hammer every fcagent at startup.
func (r *ShellSessionReaper) Run(ctx context.Context) {
	if !r.cfg.Enabled {
		logger.Info("session_reaper: disabled via config; not running")
		return
	}
	if r.manager == nil {
		logger.Warn("session_reaper: nil manager; not running")
		return
	}

	logger.WithFields(
		"idle_ttl", r.cfg.IdleTTL.String(),
		"interval", r.cfg.Interval.String(),
	).Info("session_reaper: started")

	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("session_reaper: stopped")
			return
		case <-ticker.C:
			r.sweep(ctx)
		}
	}
}

// sweep performs one pass: enumerate active sandboxes, list their
// shell sessions, kill any that match the idle predicate. Errors on
// individual sandboxes are logged but never abort the sweep — a
// flaky agent shouldn't block reclamation everywhere else.
func (r *ShellSessionReaper) sweep(ctx context.Context) {
	start := time.Now()

	var inspectedSandboxes, inspectedSessions, killed int
	for _, inst := range r.manager.snapshotInstancesForReap() {
		// Skip sandboxes that aren't actually attachable. Sleeping
		// VMs hold their tmux sessions in a snapshot — waking them
		// just to reap is the opposite of what an idle cleanup
		// should do.
		if !sandboxReachableForSessionReap(inst) {
			continue
		}
		inspectedSandboxes++

		sessions, err := r.manager.ListShellSessions(ctx, inst.ID)
		if err != nil {
			logger.WithFields("sandbox_id", inst.ID, "error", err.Error()).
				Debug("session_reaper: list failed; skipping sandbox")
			continue
		}
		for _, s := range sessions {
			inspectedSessions++
			if !shouldReap(s, r.cfg.IdleTTL) {
				continue
			}
			if err := r.manager.KillShellSession(ctx, inst.ID, s.ID); err != nil {
				logger.WithFields(
					"sandbox_id", inst.ID,
					"session_id", s.ID,
					"idle_seconds", s.IdleSeconds,
					"error", err.Error(),
				).Warn("session_reaper: kill failed")
				continue
			}
			killed++
			logger.WithFields(
				"sandbox_id", inst.ID,
				"session_id", s.ID,
				"idle_seconds", s.IdleSeconds,
			).Info("session_reaper: killed idle session")
		}
	}

	logger.WithFields(
		"sandboxes_inspected", inspectedSandboxes,
		"sessions_inspected", inspectedSessions,
		"sessions_killed", killed,
		"elapsed_ms", time.Since(start).Milliseconds(),
	).Debug("session_reaper: sweep complete")
}

// shouldReap is the predicate isolated for unit testing. Returns
// true when:
//
//  1. No clients are currently attached (so we don't yank from under
//     an active user).
//  2. IdleSeconds is a known, non-negative value (>= 0). When the
//     guest didn't report last-activity (IdleSeconds == -1) we treat
//     it as unknown and leave the session alone — fail-safe over
//     fail-loud.
//  3. IdleSeconds >= IdleTTL.
//
// Pure function so the unit test can cover every branch without
// spinning up a manager.
func shouldReap(s ShellSessionInfo, idleTTL time.Duration) bool {
	if s.AttachedClients > 0 {
		return false
	}
	if s.IdleSeconds < 0 {
		return false
	}
	return time.Duration(s.IdleSeconds)*time.Second >= idleTTL
}

// sandboxReachableForSessionReap returns true when a sandbox is in a
// lifecycle state that supports vsock RPCs. Sleeping / stopped
// instances skip cleanup because their tmux state is frozen in a
// snapshot; waking them just to reap defeats the cost-saving point
// of sleep mode.
func sandboxReachableForSessionReap(inst *Instance) bool {
	if inst == nil {
		return false
	}
	switch inst.LifecycleState {
	case LifecycleStopped, LifecycleStopping, LifecycleTerminating, LifecycleTerminated, LifecycleFailed, LifecycleReviving:
		return false
	}
	return inst.Status == StatusRunning
}
