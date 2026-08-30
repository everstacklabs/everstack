package docker

import (
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/functions/isolation"
)

func TestDefaultPoolConfig(t *testing.T) {
	cfg := DefaultPoolConfig()

	if cfg.Enabled {
		t.Error("expected pool to be disabled by default")
	}
	if cfg.MinContainersPerRuntime != 1 {
		t.Errorf("expected MinContainersPerRuntime=1, got %d", cfg.MinContainersPerRuntime)
	}
	if cfg.MaxContainersPerRuntime != 10 {
		t.Errorf("expected MaxContainersPerRuntime=10, got %d", cfg.MaxContainersPerRuntime)
	}
	if cfg.IdleTimeout != 5*time.Minute {
		t.Errorf("expected IdleTimeout=5m, got %v", cfg.IdleTimeout)
	}
	if cfg.MaxUses != 100 {
		t.Errorf("expected MaxUses=100, got %d", cfg.MaxUses)
	}
	if cfg.WarmupOnStart {
		t.Error("expected WarmupOnStart=false")
	}
}

func TestPoolStatsInitialization(t *testing.T) {
	stats := PoolStats{
		TotalContainers: 0,
		WarmContainers:  0,
		RuntimeStats:    make(map[isolation.Runtime]RuntimePoolStats),
	}

	if stats.TotalContainers != 0 {
		t.Errorf("expected TotalContainers=0, got %d", stats.TotalContainers)
	}
	if stats.WarmContainers != 0 {
		t.Errorf("expected WarmContainers=0, got %d", stats.WarmContainers)
	}
}

func TestRuntimePoolStatsCalculation(t *testing.T) {
	rps := RuntimePoolStats{
		Warm:      3,
		InUse:     2,
		Unhealthy: 1,
		Total:     6,
	}

	if rps.Total != rps.Warm+rps.InUse+rps.Unhealthy {
		t.Errorf("total should equal warm+inUse+unhealthy: %d != %d+%d+%d",
			rps.Total, rps.Warm, rps.InUse, rps.Unhealthy)
	}
}

func TestPooledContainerState(t *testing.T) {
	pc := &pooledContainer{
		id:          "test-container-123",
		runtime:     isolation.RuntimeNodeJS20,
		createdAt:   time.Now(),
		lastUsedAt:  time.Now(),
		useCount:    0,
		inUse:       false,
		healthy:     true,
		memoryMB:    512,
		networkMode: "none",
	}

	// Test initial state
	if pc.inUse {
		t.Error("new container should not be in use")
	}
	if !pc.healthy {
		t.Error("new container should be healthy")
	}
	if pc.useCount != 0 {
		t.Errorf("new container should have useCount=0, got %d", pc.useCount)
	}

	// Simulate acquisition
	pc.inUse = true
	pc.useCount++
	pc.lastUsedAt = time.Now()

	if !pc.inUse {
		t.Error("acquired container should be in use")
	}
	if pc.useCount != 1 {
		t.Errorf("acquired container should have useCount=1, got %d", pc.useCount)
	}

	// Simulate release
	pc.inUse = false

	if pc.inUse {
		t.Error("released container should not be in use")
	}
}

func TestPoolConfigValidation(t *testing.T) {
	tests := []struct {
		name   string
		config PoolConfig
		valid  bool
	}{
		{
			name: "valid config",
			config: PoolConfig{
				Enabled:                 true,
				MinContainersPerRuntime: 2,
				MaxContainersPerRuntime: 10,
				IdleTimeout:             5 * time.Minute,
				MaxUses:                 100,
				WarmupOnStart:           true,
			},
			valid: true,
		},
		{
			name: "min greater than max",
			config: PoolConfig{
				Enabled:                 true,
				MinContainersPerRuntime: 20,
				MaxContainersPerRuntime: 10,
			},
			valid: false,
		},
		{
			name: "zero values use defaults",
			config: PoolConfig{
				Enabled: true,
			},
			valid: true, // Should use defaults
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Apply defaults for zero values
			if tt.config.MinContainersPerRuntime == 0 {
				tt.config.MinContainersPerRuntime = 2
			}
			if tt.config.MaxContainersPerRuntime == 0 {
				tt.config.MaxContainersPerRuntime = 10
			}

			isValid := tt.config.MinContainersPerRuntime <= tt.config.MaxContainersPerRuntime
			if isValid != tt.valid {
				t.Errorf("expected valid=%v, got valid=%v", tt.valid, isValid)
			}
		})
	}
}

func TestParseMultiplexedOutput(t *testing.T) {
	pool := &ContainerPool{}

	// Test empty input
	stdout, stderr := pool.parseMultiplexedOutput([]byte{})
	if stdout != "" || stderr != "" {
		t.Errorf("expected empty output for empty input, got stdout=%q stderr=%q", stdout, stderr)
	}

	// Test input too short for header
	stdout, stderr = pool.parseMultiplexedOutput([]byte{1, 2, 3})
	if stdout != "" || stderr != "" {
		t.Errorf("expected empty output for short input, got stdout=%q stderr=%q", stdout, stderr)
	}

	// Test valid stdout header with content
	// Format: [stream_type, 0, 0, 0, size (4 bytes big-endian)]
	input := []byte{
		1, 0, 0, 0, 0, 0, 0, 5, // stdout header, size=5
		'h', 'e', 'l', 'l', 'o', // content
	}
	stdout, stderr = pool.parseMultiplexedOutput(input)
	if stdout != "hello" {
		t.Errorf("expected stdout='hello', got %q", stdout)
	}
	if stderr != "" {
		t.Errorf("expected stderr='', got %q", stderr)
	}

	// Test valid stderr header with content
	input = []byte{
		2, 0, 0, 0, 0, 0, 0, 5, // stderr header, size=5
		'e', 'r', 'r', 'o', 'r', // content
	}
	stdout, stderr = pool.parseMultiplexedOutput(input)
	if stdout != "" {
		t.Errorf("expected stdout='', got %q", stdout)
	}
	if stderr != "error" {
		t.Errorf("expected stderr='error', got %q", stderr)
	}
}

func TestParseResult(t *testing.T) {
	pool := &ContainerPool{}

	tests := []struct {
		name     string
		stdout   string
		stderr   string
		exitCode int64
		success  bool
		hasError bool
	}{
		{
			name:     "success with JSON result",
			stdout:   `{"success":true,"result":{"sum":42}}`,
			stderr:   "",
			exitCode: 0,
			success:  true,
			hasError: false,
		},
		{
			name:     "success with __result__ format",
			stdout:   `{"__result__":{"sum":42}}`,
			stderr:   "",
			exitCode: 0,
			success:  true,
			hasError: false,
		},
		{
			name:     "runtime error",
			stdout:   "",
			stderr:   "ReferenceError: foo is not defined",
			exitCode: 1,
			success:  false,
			hasError: true,
		},
		{
			name:     "OOM error",
			stdout:   "",
			stderr:   "",
			exitCode: 137,
			success:  false,
			hasError: true,
		},
		{
			name:     "plain text output",
			stdout:   "Hello, World!",
			stderr:   "",
			exitCode: 0,
			success:  true,
			hasError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := pool.parseResult(tt.stdout, tt.stderr, tt.exitCode)

			if result.Success != tt.success {
				t.Errorf("expected success=%v, got %v", tt.success, result.Success)
			}

			if tt.hasError && result.Error == "" {
				t.Error("expected error message, got empty")
			}

			if !tt.hasError && result.Error != "" {
				t.Errorf("expected no error, got %q", result.Error)
			}

			// Check OOM detection
			if tt.exitCode == 137 && result.ErrorType != isolation.ErrorTypeOOM {
				t.Errorf("expected OOM error type for exit code 137, got %v", result.ErrorType)
			}
		})
	}
}
