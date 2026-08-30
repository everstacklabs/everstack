package sandbox

import "testing"

func TestResolveWorkDir(t *testing.T) {
	if got := resolveWorkDir(""); got != DefaultWorkDir {
		t.Fatalf("empty: got %q, want %q", got, DefaultWorkDir)
	}
	if got := resolveWorkDir("   "); got != DefaultWorkDir {
		t.Fatalf("whitespace: got %q, want %q", got, DefaultWorkDir)
	}
	if got := resolveWorkDir("/workspace"); got != "/workspace" {
		t.Fatalf("explicit: got %q, want /workspace", got)
	}
	if got := resolveWorkDir("  /repo  "); got != "/repo" {
		t.Fatalf("trims: got %q, want /repo", got)
	}
}

func TestDefaultGlobalSandboxConfigInheritsDNS(t *testing.T) {
	cfg := DefaultGlobalSandboxConfig()
	if len(cfg.DNSServers) != 0 {
		t.Fatalf("default DNS servers = %v, want empty so backends inherit runtime DNS", cfg.DNSServers)
	}
}

func TestParseSandboxConfig_WorkDir(t *testing.T) {
	// 1. Explicit sandbox.work_dir wins.
	cfg := ParseSandboxConfig(map[string]interface{}{
		"working_directory": "/from-toplevel",
		"sandbox": map[string]interface{}{
			"enabled":  true,
			"work_dir": "/from-sandbox",
		},
	})
	if cfg.WorkDir != "/from-sandbox" {
		t.Fatalf("precedence: got %q, want /from-sandbox", cfg.WorkDir)
	}

	// 2. Top-level working_directory used when sandbox.work_dir is absent.
	cfg = ParseSandboxConfig(map[string]interface{}{
		"working_directory": "/from-toplevel",
		"sandbox": map[string]interface{}{
			"enabled": true,
		},
	})
	if cfg.WorkDir != "/from-toplevel" {
		t.Fatalf("fallback to toplevel: got %q, want /from-toplevel", cfg.WorkDir)
	}

	// 3. Neither set → leaves WorkDir empty so resolveWorkDir can apply default.
	cfg = ParseSandboxConfig(map[string]interface{}{
		"sandbox": map[string]interface{}{
			"enabled": true,
		},
	})
	if cfg.WorkDir != "" {
		t.Fatalf("unset: got %q, want empty (so caller defaults)", cfg.WorkDir)
	}

	// 4. Whitespace-only values do not override.
	cfg = ParseSandboxConfig(map[string]interface{}{
		"working_directory": "  ",
		"sandbox": map[string]interface{}{
			"enabled":  true,
			"work_dir": "\t",
		},
	})
	if cfg.WorkDir != "" {
		t.Fatalf("whitespace: got %q, want empty", cfg.WorkDir)
	}
}
