package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

// ManagerBackend adapts SandboxManager without exposing its broader surface to
// the OpenAI session service.
type ManagerBackend struct{ manager *sandbox.SandboxManager }

func NewManagerBackend(manager *sandbox.SandboxManager) *ManagerBackend {
	return &ManagerBackend{manager: manager}
}

func (b *ManagerBackend) Provision(ctx context.Context, owner Ownership, sessionKey string, cfg sandbox.SandboxConfig) (*sandbox.Instance, error) {
	if b == nil || b.manager == nil {
		return nil, errors.New("sandbox manager is not configured")
	}
	if !SupportedBackend(b.manager.BackendName()) {
		return nil, fmt.Errorf("sandbox backend %q cannot inject the executor credential without placing it in command arguments", b.manager.BackendName())
	}
	inst, err := b.manager.GetOrCreateForInstance(ctx, sessionKey, owner.InstanceID, owner.InstanceID, cfg)
	if err != nil {
		return nil, err
	}
	bound, err := b.manager.BindOpenAIExecutorOwnership(ctx, inst.ID, owner.InstanceID, owner.InstanceID)
	if err != nil {
		return inst, err
	}
	return bound, nil
}

// SupportedBackend reports the CE backends whose Exec implementation carries
// environment variables separately from argv. Kubernetes currently prefixes
// argv with `env KEY=value`, which can expose the executor key in API audit
// records and is therefore rejected for this integration.
func SupportedBackend(name string) bool {
	switch name {
	case "firecracker", "firecracker-agent":
		return true
	default:
		return false
	}
}

func (b *ManagerBackend) VerifyOwnership(ctx context.Context, owner Ownership, sandboxID string) error {
	if b == nil || b.manager == nil {
		return errors.New("sandbox manager is not configured")
	}
	_, err := b.manager.LookupOpenAIExecutorSandbox(ctx, sandboxID, owner.InstanceID, owner.InstanceID)
	return err
}

func (b *ManagerBackend) Exec(ctx context.Context, owner Ownership, sandboxID string, cmd Command) (*sandbox.ExecResult, error) {
	if err := b.VerifyOwnership(ctx, owner, sandboxID); err != nil {
		return nil, err
	}
	return b.manager.ExecBySandboxID(ctx, sandboxID, sandbox.ExecRequest{
		Command: append([]string(nil), cmd.Argv...), WorkDir: cmd.WorkDir,
		Env: cloneStrings(cmd.Env), Timeout: cmd.Timeout, SilentLog: cmd.SilentLog,
	})
}

func (b *ManagerBackend) ReadFile(ctx context.Context, owner Ownership, sandboxID, name string) ([]byte, error) {
	resolved, err := b.resolveGuestPath(ctx, owner, sandboxID, name)
	if err != nil {
		return nil, err
	}
	return b.manager.ReadFileBySandboxID(ctx, sandboxID, resolved)
}

func (b *ManagerBackend) WriteFile(ctx context.Context, owner Ownership, sandboxID, name string, data []byte) error {
	resolved, err := b.resolveGuestPath(ctx, owner, sandboxID, name)
	if err != nil {
		return err
	}
	return b.manager.WriteFileBySandboxID(ctx, sandboxID, resolved, data)
}

func (b *ManagerBackend) ListFiles(ctx context.Context, owner Ownership, sandboxID, name string) ([]sandbox.FileInfo, error) {
	resolved, err := b.resolveGuestPath(ctx, owner, sandboxID, name)
	if err != nil {
		return nil, err
	}
	return b.manager.ListFilesBySandboxID(ctx, sandboxID, resolved)
}

func (b *ManagerBackend) Destroy(ctx context.Context, owner Ownership, sandboxID, sessionKey string) error {
	if b == nil || b.manager == nil {
		return errors.New("sandbox manager is not configured")
	}
	inst, err := b.manager.LookupOpenAIExecutorSandbox(ctx, sandboxID, owner.InstanceID, owner.InstanceID)
	if err != nil {
		return err
	}
	if !inst.DestroyedAt.IsZero() || inst.LifecycleState == sandbox.LifecycleTerminated {
		return nil
	}
	return b.manager.DestroyWithReason(ctx, sessionKey, "openai_agents_api_environment_cleanup")
}

func (b *ManagerBackend) resolveGuestPath(ctx context.Context, owner Ownership, sandboxID, name string) (string, error) {
	candidate, err := ResolveWorkspacePath(name)
	if err != nil {
		return "", err
	}
	result, err := b.Exec(ctx, owner, sandboxID, Command{
		Argv: []string{"realpath", "-m", "--", candidate}, WorkDir: WorkspaceDirectory,
		Timeout: 10 * time.Second, SilentLog: true,
	})
	if err != nil {
		return "", fmt.Errorf("resolve sandbox workspace path: %w", err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("resolve sandbox workspace path: %s", sanitizeMessage(result.Stderr))
	}
	resolved, err := resolvedWorkspacePath(result.Stdout)
	if err != nil {
		return "", errors.New("workspace path resolves outside /workspace")
	}
	return resolved, nil
}

func resolvedWorkspacePath(realpathOutput string) (string, error) {
	return ResolveWorkspacePath(strings.TrimSpace(realpathOutput))
}

func cloneStrings(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

var _ Backend = (*ManagerBackend)(nil)
