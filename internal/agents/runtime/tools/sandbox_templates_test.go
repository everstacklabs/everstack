package tools

import (
	"reflect"
	"testing"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

func TestInstanceMatchesTemplate(t *testing.T) {
	t.Parallel()

	cfg := sandbox.SandboxConfig{
		Image:       "node:24-slim",
		CPULimit:    1,
		MemoryMB:    512,
		DiskMB:      1024,
		NetworkMode: "whitelist",
		AllowedHosts: []string{
			"registry.npmjs.org",
			"pypi.org",
		},
		EnvVars: map[string]string{"NODE_ENV": "development"},
	}

	inst := &sandbox.Instance{
		Config: sandbox.InstanceConfig{
			Image:       cfg.Image,
			CPULimit:    cfg.CPULimit,
			MemoryMB:    cfg.MemoryMB,
			DiskMB:      cfg.DiskMB,
			NetworkMode: sandbox.NetworkMode(cfg.NetworkMode),
			AllowedHosts: []string{
				"pypi.org",
				"registry.npmjs.org", // different order should still match
			},
			EnvVars: map[string]string{"NODE_ENV": "development"},
		},
	}

	if !instanceMatchesTemplate(inst, cfg) {
		t.Fatalf("expected instance to match template config")
	}

	cfg2 := cfg
	cfg2.Image = "python:3.12"
	if instanceMatchesTemplate(inst, cfg2) {
		t.Fatalf("expected image mismatch to fail")
	}
}

func TestApplyTemplatePreservingSessionFields(t *testing.T) {
	t.Parallel()

	templateCfg := sandbox.SandboxConfig{
		Enabled:              true,
		Image:                "everstacklabs/sandbox:node-dev",
		CPULimit:             1,
		MemoryMB:             512,
		DiskMB:               1024,
		TimeoutSeconds:       300,
		NetworkMode:          "whitelist",
		AllowedHosts:         []string{"registry.npmjs.org"},
		EnvVars:              map[string]string{"NODE_ENV": "development"},
		IdleRetentionSeconds: 3600,
		Name:                 "template-name",
	}
	existing := sandbox.SandboxConfig{
		Enabled:              true,
		Image:                "custom/image",
		CPULimit:             2,
		MemoryMB:             2048,
		DiskMB:               2048,
		TimeoutSeconds:       600,
		NetworkMode:          "allow",
		AllowedHosts:         []string{"example.com"},
		EnvVars:              map[string]string{"EXTRA": "1"},
		GitRepoURL:           "openmodels/model-catalog",
		GitBranch:            "main",
		GitInstallationID:    12345,
		IdleRetentionSeconds: 7200,
		Name:                 "agent-sandbox",
		SSHEnabled:           true,
		Tools:                []string{"sandbox_shell", "sandbox_list_files"},
	}

	merged := applyTemplatePreservingSessionFields(templateCfg, existing)

	// Template-selected runtime values should be applied.
	if merged.Image != templateCfg.Image || merged.NetworkMode != templateCfg.NetworkMode {
		t.Fatalf("expected template runtime settings to be applied")
	}

	// Session metadata should be preserved.
	if merged.GitRepoURL != existing.GitRepoURL || merged.GitBranch != existing.GitBranch || merged.GitInstallationID != existing.GitInstallationID {
		t.Fatalf("expected git settings to be preserved")
	}
	if merged.IdleRetentionSeconds != existing.IdleRetentionSeconds {
		t.Fatalf("expected idle retention to be preserved")
	}
	if merged.Name != existing.Name {
		t.Fatalf("expected name to be preserved")
	}
	if merged.SSHEnabled != existing.SSHEnabled {
		t.Fatalf("expected ssh setting to be preserved")
	}
	if !reflect.DeepEqual(merged.Tools, existing.Tools) {
		t.Fatalf("expected tool allowlist to be preserved")
	}
}
