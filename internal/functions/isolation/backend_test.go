package isolation

import (
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.DefaultTimeoutMS != 30000 {
		t.Errorf("expected timeout 30000, got %d", cfg.DefaultTimeoutMS)
	}
	if cfg.DefaultMemoryMB != 512 {
		t.Errorf("expected memory 512, got %d", cfg.DefaultMemoryMB)
	}
	if cfg.DefaultVCPUs != 1 {
		t.Errorf("expected vcpus 1, got %d", cfg.DefaultVCPUs)
	}
	if cfg.DefaultNetworkMode != NetworkDeny {
		t.Errorf("expected network mode deny, got %s", cfg.DefaultNetworkMode)
	}
}

func TestConfigApplyDefaults(t *testing.T) {
	cfg := DefaultConfig()

	// Empty request should get all defaults
	req := &ExecutionRequest{}
	cfg.ApplyDefaults(req)

	if req.TimeoutMS != 30000 {
		t.Errorf("expected timeout 30000, got %d", req.TimeoutMS)
	}
	if req.MemoryMB != 512 {
		t.Errorf("expected memory 512, got %d", req.MemoryMB)
	}
	if req.VCPUs != 1 {
		t.Errorf("expected vcpus 1, got %d", req.VCPUs)
	}
	if req.NetworkMode != NetworkDeny {
		t.Errorf("expected network mode deny, got %s", req.NetworkMode)
	}

	// Request with values should keep them
	req2 := &ExecutionRequest{
		TimeoutMS:   5000,
		MemoryMB:    1024,
		VCPUs:       2,
		NetworkMode: NetworkAllow,
	}
	cfg.ApplyDefaults(req2)

	if req2.TimeoutMS != 5000 {
		t.Errorf("expected timeout 5000, got %d", req2.TimeoutMS)
	}
	if req2.MemoryMB != 1024 {
		t.Errorf("expected memory 1024, got %d", req2.MemoryMB)
	}
	if req2.VCPUs != 2 {
		t.Errorf("expected vcpus 2, got %d", req2.VCPUs)
	}
	if req2.NetworkMode != NetworkAllow {
		t.Errorf("expected network mode allow, got %s", req2.NetworkMode)
	}
}

func TestExecutionRequestTimeout(t *testing.T) {
	tests := []struct {
		timeoutMS int
		expected  time.Duration
	}{
		{0, 30 * time.Second},
		{-1, 30 * time.Second},
		{5000, 5 * time.Second},
		{60000, 60 * time.Second},
	}

	for _, tt := range tests {
		req := &ExecutionRequest{TimeoutMS: tt.timeoutMS}
		got := req.ExecutionTimeout()
		if got != tt.expected {
			t.Errorf("ExecutionTimeout(%d) = %v, want %v", tt.timeoutMS, got, tt.expected)
		}
	}
}

func TestValidRuntimes(t *testing.T) {
	runtimes := ValidRuntimes()

	if len(runtimes) != 3 {
		t.Errorf("expected 3 runtimes, got %d", len(runtimes))
	}

	found := make(map[Runtime]bool)
	for _, r := range runtimes {
		found[r] = true
	}

	if !found[RuntimeNodeJS20] {
		t.Error("missing RuntimeNodeJS20")
	}
	if !found[RuntimeDeno] {
		t.Error("missing RuntimeDeno")
	}
	if !found[RuntimePython3] {
		t.Error("missing RuntimePython3")
	}
}

func TestNetworkModeConstants(t *testing.T) {
	if NetworkDeny != "deny" {
		t.Errorf("expected deny, got %s", NetworkDeny)
	}
	if NetworkWhitelist != "whitelist" {
		t.Errorf("expected whitelist, got %s", NetworkWhitelist)
	}
	if NetworkAllow != "allow" {
		t.Errorf("expected allow, got %s", NetworkAllow)
	}
}

func TestErrorTypeConstants(t *testing.T) {
	if ErrorTypeNone != "" {
		t.Errorf("expected empty, got %s", ErrorTypeNone)
	}
	if ErrorTypeTimeout != "timeout" {
		t.Errorf("expected timeout, got %s", ErrorTypeTimeout)
	}
	if ErrorTypeOOM != "oom" {
		t.Errorf("expected oom, got %s", ErrorTypeOOM)
	}
	if ErrorTypeSyntax != "syntax" {
		t.Errorf("expected syntax, got %s", ErrorTypeSyntax)
	}
	if ErrorTypeRuntime != "runtime" {
		t.Errorf("expected runtime, got %s", ErrorTypeRuntime)
	}
	if ErrorTypeNetwork != "network" {
		t.Errorf("expected network, got %s", ErrorTypeNetwork)
	}
}
