package adk

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

// managerSandbox is the production Sandbox backed by Everstack's SandboxManager.
type managerSandbox struct {
	mgr          *sandbox.SandboxManager
	image        string
	networkMode  string
	allowedHosts []string
	envVars      map[string]string
}

// AdapterConfig configures the SandboxManager-backed ADK sandbox.
type AdapterConfig struct {
	// Image is the python base image (pip available); empty -> python:3.12-slim.
	Image string
	// NetworkMode controls sandbox egress. ADK needs *some* egress (pip install
	// google-adk + reaching the model endpoint), but we never hardcode wide-open
	// "allow": full egress from caller-supplied code is an SSRF/exfiltration path
	// to internal services. On multi-tenant cloud this MUST be "whitelist".
	NetworkMode string
	// AllowedHosts is the egress allowlist used with NetworkMode "whitelist".
	// When empty under whitelist, the package-registry defaults are applied so
	// pip install still works; the model endpoint host should be appended here.
	AllowedHosts []string
	// EnvVars are injected into the sandbox (e.g. the model base URL + key so the
	// ADK agent reaches models through the Everstack gateway).
	EnvVars map[string]string
}

// NewSandboxManagerAdapter wraps a SandboxManager as an adk.Sandbox.
func NewSandboxManagerAdapter(mgr *sandbox.SandboxManager, cfg AdapterConfig) Sandbox {
	image := cfg.Image
	if image == "" {
		image = "python:3.12-slim"
	}
	return &managerSandbox{
		mgr:          mgr,
		image:        image,
		networkMode:  cfg.NetworkMode,
		allowedHosts: cfg.AllowedHosts,
		envVars:      cfg.EnvVars,
	}
}

func (m *managerSandbox) Create(ctx context.Context, tenantID string) (string, error) {
	handle := randHandle()
	cfg := sandbox.SandboxConfig{
		Enabled:        true,
		Image:          m.image,
		MemoryMB:       1024,
		TimeoutSeconds: 600,
		Name:           handle,
		WorkDir:        workDir,
		EnvVars:        m.envVars,
	}
	// Only set egress policy when explicitly configured; otherwise defer to the
	// manager's tenant-clamped default rather than forcing wide-open access.
	if m.networkMode != "" {
		cfg.NetworkMode = m.networkMode
		cfg.AllowedHosts = m.allowedHosts
		// whitelist with no hosts would block everything (incl. PyPI); the
		// SandboxManager path doesn't apply the package-registry fallback that
		// ParseSandboxConfig does, so apply it here or pip install google-adk fails.
		if m.networkMode == "whitelist" && len(cfg.AllowedHosts) == 0 {
			cfg.AllowedHosts = sandbox.DefaultAllowedHosts()
		}
	}
	if _, err := m.mgr.GetOrCreate(ctx, handle, tenantID, cfg); err != nil {
		return "", err
	}
	return handle, nil
}

func (m *managerSandbox) WriteFile(ctx context.Context, handle, path, content string) error {
	// Write via base64 to stay backend-agnostic and binary-safe: base64 output
	// is shell-safe, so single-quoting it is sufficient.
	b64 := base64.StdEncoding.EncodeToString([]byte(content))
	script := fmt.Sprintf("mkdir -p \"$(dirname '%s')\" && printf '%%s' '%s' | base64 -d > '%s'", path, b64, path)
	res, err := m.mgr.Exec(ctx, handle, sandbox.ExecRequest{
		Command: []string{"sh", "-c", script},
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("write %s failed (exit %d): %s", path, res.ExitCode, res.Stderr)
	}
	return nil
}

func (m *managerSandbox) Exec(ctx context.Context, handle string, cmd []string) (string, string, int, error) {
	res, err := m.mgr.Exec(ctx, handle, sandbox.ExecRequest{
		Command: cmd,
		WorkDir: workDir,
		Timeout: 5 * time.Minute,
	})
	if err != nil {
		return "", "", -1, err
	}
	return res.Stdout, res.Stderr, res.ExitCode, nil
}

func (m *managerSandbox) Destroy(ctx context.Context, handle string) error {
	return m.mgr.Destroy(ctx, handle)
}
