package sandbox

import (
	"testing"
	"time"
)

func TestShouldReap(t *testing.T) {
	const idleTTL = 24 * time.Hour

	cases := []struct {
		name string
		info ShellSessionInfo
		want bool
	}{
		{
			name: "attached session never reaped",
			info: ShellSessionInfo{
				AttachedClients: 1,
				IdleSeconds:     int64((48 * time.Hour).Seconds()),
			},
			want: false,
		},
		{
			name: "unknown idle (negative) never reaped",
			info: ShellSessionInfo{
				AttachedClients: 0,
				IdleSeconds:     -1,
			},
			want: false,
		},
		{
			name: "idle below TTL not reaped",
			info: ShellSessionInfo{
				AttachedClients: 0,
				IdleSeconds:     int64((23 * time.Hour).Seconds()),
			},
			want: false,
		},
		{
			name: "idle equal to TTL is reaped",
			info: ShellSessionInfo{
				AttachedClients: 0,
				IdleSeconds:     int64(idleTTL.Seconds()),
			},
			want: true,
		},
		{
			name: "idle well past TTL is reaped",
			info: ShellSessionInfo{
				AttachedClients: 0,
				IdleSeconds:     int64((72 * time.Hour).Seconds()),
			},
			want: true,
		},
		{
			name: "zero-second idle (just activity) not reaped",
			info: ShellSessionInfo{
				AttachedClients: 0,
				IdleSeconds:     0,
			},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldReap(tc.info, idleTTL)
			if got != tc.want {
				t.Errorf("shouldReap(%+v, %s) = %v, want %v", tc.info, idleTTL, got, tc.want)
			}
		})
	}
}

func TestSandboxReachableForSessionReap(t *testing.T) {
	cases := []struct {
		name string
		inst *Instance
		want bool
	}{
		{name: "nil", inst: nil, want: false},
		{
			name: "running",
			inst: &Instance{Status: StatusRunning, LifecycleState: LifecycleRunning},
			want: true,
		},
		{
			name: "running with empty lifecycle (legacy)",
			inst: &Instance{Status: StatusRunning, LifecycleState: ""},
			want: true,
		},
		{
			name: "stopped",
			inst: &Instance{Status: StatusRunning, LifecycleState: LifecycleStopped},
			want: false,
		},
		{
			name: "sleeping (== stopped lifecycle string)",
			inst: &Instance{Status: StatusRunning, LifecycleState: "sleeping"},
			// "sleeping" is not in the explicit deny list; reaper
			// treats it as "running" today. This test pins current
			// behavior so a future change is intentional, not
			// accidental. See plan doc for rationale.
			want: true,
		},
		{
			name: "terminating",
			inst: &Instance{Status: StatusRunning, LifecycleState: LifecycleTerminating},
			want: false,
		},
		{
			name: "reviving",
			inst: &Instance{Status: StatusRunning, LifecycleState: LifecycleReviving},
			want: false,
		},
		{
			name: "pending status",
			inst: &Instance{Status: StatusPending, LifecycleState: LifecycleRunning},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sandboxReachableForSessionReap(tc.inst)
			if got != tc.want {
				t.Errorf("sandboxReachableForSessionReap(%+v) = %v, want %v", tc.inst, got, tc.want)
			}
		})
	}
}

func TestNewShellSessionReaperFromEnv_Defaults(t *testing.T) {
	// Clear any inherited env so the test is hermetic.
	t.Setenv("EVERSTACK_SHELL_SESSION_IDLE_TTL", "")
	t.Setenv("EVERSTACK_SHELL_SESSION_REAPER_INTERVAL", "")
	t.Setenv("EVERSTACK_SHELL_SESSION_REAPER_ENABLED", "")

	cfg := NewShellSessionReaperFromEnv()
	def := DefaultShellSessionReaperConfig()
	if cfg.IdleTTL != def.IdleTTL {
		t.Errorf("IdleTTL: got %s, want %s", cfg.IdleTTL, def.IdleTTL)
	}
	if cfg.Interval != def.Interval {
		t.Errorf("Interval: got %s, want %s", cfg.Interval, def.Interval)
	}
	if cfg.Enabled != def.Enabled {
		t.Errorf("Enabled: got %v, want %v", cfg.Enabled, def.Enabled)
	}
}

func TestNewShellSessionReaperFromEnv_Overrides(t *testing.T) {
	t.Setenv("EVERSTACK_SHELL_SESSION_IDLE_TTL", "30m")
	t.Setenv("EVERSTACK_SHELL_SESSION_REAPER_INTERVAL", "5m")
	t.Setenv("EVERSTACK_SHELL_SESSION_REAPER_ENABLED", "false")

	cfg := NewShellSessionReaperFromEnv()
	if cfg.IdleTTL != 30*time.Minute {
		t.Errorf("IdleTTL: got %s, want 30m", cfg.IdleTTL)
	}
	if cfg.Interval != 5*time.Minute {
		t.Errorf("Interval: got %s, want 5m", cfg.Interval)
	}
	if cfg.Enabled {
		t.Errorf("Enabled: got true, want false")
	}
}

func TestNewShellSessionReaperFromEnv_InvalidFallsBack(t *testing.T) {
	t.Setenv("EVERSTACK_SHELL_SESSION_IDLE_TTL", "garbage")
	t.Setenv("EVERSTACK_SHELL_SESSION_REAPER_INTERVAL", "-1h")
	t.Setenv("EVERSTACK_SHELL_SESSION_REAPER_ENABLED", "not-a-bool")

	cfg := NewShellSessionReaperFromEnv()
	def := DefaultShellSessionReaperConfig()
	if cfg.IdleTTL != def.IdleTTL {
		t.Errorf("invalid IdleTTL should fall back, got %s", cfg.IdleTTL)
	}
	if cfg.Interval != def.Interval {
		t.Errorf("invalid Interval should fall back, got %s", cfg.Interval)
	}
	if !cfg.Enabled {
		t.Errorf("invalid Enabled should keep default true, got false")
	}
}
