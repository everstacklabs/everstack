package executor

import (
	"context"
	"testing"

	"github.com/everstacklabs/everstack/internal/functions/isolation"
)

func TestMapRuntime(t *testing.T) {
	tests := []struct {
		input    string
		expected isolation.Runtime
	}{
		{"nodejs20", isolation.RuntimeNodeJS20},
		{"node20", isolation.RuntimeNodeJS20},
		{"node", isolation.RuntimeNodeJS20},
		{"deno", isolation.RuntimeDeno},
		{"python", isolation.RuntimePython3},
		{"python3", isolation.RuntimePython3},
		{"unknown", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := mapRuntime(tt.input)
			if result != tt.expected {
				t.Errorf("mapRuntime(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestMapNetworkMode(t *testing.T) {
	tests := []struct {
		input    string
		expected isolation.NetworkMode
	}{
		{"deny", isolation.NetworkDeny},
		{"whitelist", isolation.NetworkWhitelist},
		{"allow", isolation.NetworkAllow},
		{"", isolation.NetworkDeny},
		{"unknown", isolation.NetworkDeny},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := mapNetworkMode(tt.input)
			if result != tt.expected {
				t.Errorf("mapNetworkMode(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsolatedExecutorMode(t *testing.T) {
	executor := &IsolatedExecutor{}
	if executor.Mode() != ModeIsolated {
		t.Errorf("expected mode %s, got %s", ModeIsolated, executor.Mode())
	}
}

func TestIsolatedExecutorBackendName(t *testing.T) {
	// With nil backend
	executor := &IsolatedExecutor{backend: nil}
	if executor.BackendName() != "none" {
		t.Errorf("expected backend name 'none', got %s", executor.BackendName())
	}

	// With mock backend
	mockBackend := &mockBackend{name: "test"}
	executor = &IsolatedExecutor{backend: mockBackend}
	if executor.BackendName() != "test" {
		t.Errorf("expected backend name 'test', got %s", executor.BackendName())
	}
}

func TestIsolatedExecutorStats(t *testing.T) {
	// With nil backend
	executor := &IsolatedExecutor{backend: nil}
	stats := executor.Stats()
	if stats.Name != "" {
		t.Errorf("expected empty stats name, got %s", stats.Name)
	}

	// With mock backend
	mockBackend := &mockBackend{
		name: "test",
		stats: isolation.BackendStats{
			Name:            "test",
			ActiveRequests:  2,
			TotalExecutions: 100,
		},
	}
	executor = &IsolatedExecutor{backend: mockBackend}
	stats = executor.Stats()
	if stats.Name != "test" {
		t.Errorf("expected stats name 'test', got %s", stats.Name)
	}
	if stats.TotalExecutions != 100 {
		t.Errorf("expected total executions 100, got %d", stats.TotalExecutions)
	}
}

func TestFunctionConfigIsolatedFields(t *testing.T) {
	config := &FunctionConfig{
		ID:           "test-function",
		Name:         "test",
		Mode:         ModeIsolated,
		Runtime:      "nodejs20",
		Code:         "export default () => 42",
		Packages:     []string{"lodash", "axios"},
		NetworkMode:  "whitelist",
		AllowedHosts: []string{"api.example.com", "cdn.example.com"},
		VCPUs:        2,
		MemoryMB:     1024,
		TimeoutMs:    60000,
	}

	if config.Mode != ModeIsolated {
		t.Error("expected isolated mode")
	}
	if config.Runtime != "nodejs20" {
		t.Error("unexpected runtime")
	}
	if len(config.Packages) != 2 {
		t.Error("unexpected packages count")
	}
	if config.NetworkMode != "whitelist" {
		t.Error("unexpected network mode")
	}
	if len(config.AllowedHosts) != 2 {
		t.Error("unexpected allowed hosts count")
	}
	if config.VCPUs != 2 {
		t.Error("unexpected vcpus")
	}
	if config.MemoryMB != 1024 {
		t.Error("unexpected memory")
	}
}

// mockBackend implements isolation.Backend for testing.
type mockBackend struct {
	name    string
	stats   isolation.BackendStats
	started bool
	stopped bool
}

func (m *mockBackend) Name() string { return m.name }

func (m *mockBackend) Start(ctx context.Context) error {
	m.started = true
	return nil
}

func (m *mockBackend) Stop(ctx context.Context) error {
	m.stopped = true
	return nil
}

func (m *mockBackend) Execute(ctx context.Context, req isolation.ExecutionRequest) (*isolation.ExecutionResult, error) {
	return &isolation.ExecutionResult{
		Success:    true,
		Result:     "mock result",
		DurationMS: 100,
	}, nil
}

func (m *mockBackend) SupportsRuntime(runtime isolation.Runtime) bool {
	return runtime == isolation.RuntimeNodeJS20 || runtime == isolation.RuntimeDeno
}

func (m *mockBackend) Stats() isolation.BackendStats {
	return m.stats
}
