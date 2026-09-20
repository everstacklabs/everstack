// Package executor connects OpenAI Agents API self-hosted environments to
// isolated Everstack sandboxes.
package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

const (
	EnvironmentMode       = "self_hosted"
	WorkspaceDirectory    = "/workspace"
	ExecutorVersion       = "0.154.0"
	LocalExecutorImage    = "everstack/openai-codex-executor:" + ExecutorVersion
	defaultStartupTimeout = 2 * time.Minute
	defaultExecTimeout    = 24 * time.Hour
)

var requiredOpenAIHosts = []string{"api.openai.com", "codex-cloud-environments.chatgpt.com"}

// ErrNotBound means this process no longer holds the executor binding.
// Ownership violations remain separate errors.
var ErrNotBound = errors.New("environment has no local executor binding")

type Status string

const (
	StatusPending   Status = "pending"
	StatusConnected Status = "connected"
	StatusFailed    Status = "failed"
	StatusStopping  Status = "stopping"
	StatusStopped   Status = "stopped"
	StatusDeleting  Status = "deleting"
	StatusDeleted   Status = "deleted"
)

type Ownership struct {
	OrganizationID string
	InstanceID     string
}

type ProvisionRequest struct {
	Ownership
	LocalSessionID        string
	UpstreamEnvironmentID string
	RemoteURL             string
	ExecutorAPIKey        string
	WorkspaceDirectory    string
	Image                 string
	Lifetime              context.Context
	FenceStartupTimeout   func(context.Context) error
}

type Environment struct {
	SandboxID             string
	LocalSessionID        string
	UpstreamEnvironmentID string
	Status                Status
	Error                 string
	ExecutorVersion       string
}

type ObserveRequest struct {
	Ownership
	LocalSessionID string
	Status         Status
	Error          string
}

// ReconcileRequest is safe to persist. ExecutorAPIKey must be resolved from a
// secret store for each restart and must never be serialized with this record.
type ReconcileRequest struct {
	Environment
	Ownership
	RemoteURL           string
	ExecutorAPIKey      string
	Lifetime            context.Context
	FenceStartupTimeout func(context.Context) error
}

type Service interface {
	Provision(context.Context, ProvisionRequest) (*Environment, error)
	Status(context.Context, Ownership, string) (*Environment, error)
	Observe(context.Context, ObserveRequest) (*Environment, error)
	Stop(context.Context, Ownership, string) error
	Delete(context.Context, Ownership, string) error
	Relinquish(context.Context, Ownership, string) error
	DeleteReference(context.Context, Ownership, string, string) error
	Reconcile(context.Context, ReconcileRequest) (*Environment, error)
}

// Relinquish stops only this process's executor reference. The sandbox remains
// available for the next lease owner to reconcile.
func (m *Manager) Relinquish(_ context.Context, owner Ownership, localSessionID string) error {
	owner = normalizeOwner(owner)
	m.mu.Lock()
	b := m.bindings[localSessionID]
	if b == nil {
		m.mu.Unlock()
		return nil
	}
	if b.owner != owner {
		m.mu.Unlock()
		return errors.New("environment is not owned by authenticated instance")
	}
	stopTimer(b)
	b.cancel()
	// Cleanup that has already committed owns this sandbox. Keep its local
	// tombstone so a same-process lease reacquisition cannot reconcile a new
	// executor onto the sandbox while the retiring cleanup destroys it.
	if !b.retiring {
		delete(m.bindings, localSessionID)
	}
	m.mu.Unlock()
	return nil
}

// DeleteReference destroys the authenticated durable sandbox reference even
// when this process never held its executor binding.
func (m *Manager) DeleteReference(ctx context.Context, owner Ownership, localSessionID, sandboxID string) error {
	owner = normalizeOwner(owner)
	m.mu.RLock()
	local := m.bindings[localSessionID]
	m.mu.RUnlock()
	if local != nil {
		return m.stop(ctx, owner, localSessionID, true)
	}
	return m.backend.Destroy(ctx, owner, sandboxID, sandboxSessionKey(localSessionID))
}

type Command struct {
	Argv      []string
	WorkDir   string
	Env       map[string]string
	Timeout   time.Duration
	SilentLog bool
}

// Backend is the narrow test seam around Everstack's production sandbox
// manager. Every operation receives authenticated ownership explicitly.
type Backend interface {
	Provision(context.Context, Ownership, string, sandbox.SandboxConfig) (*sandbox.Instance, error)
	VerifyOwnership(context.Context, Ownership, string) error
	Exec(context.Context, Ownership, string, Command) (*sandbox.ExecResult, error)
	ReadFile(context.Context, Ownership, string, string) ([]byte, error)
	WriteFile(context.Context, Ownership, string, string, []byte) error
	ListFiles(context.Context, Ownership, string, string) ([]sandbox.FileInfo, error)
	Destroy(context.Context, Ownership, string, string) error
}

type Manager struct {
	backend        Backend
	startupTimeout time.Duration

	mu       sync.RWMutex
	bindings map[string]*binding
}

type binding struct {
	environment         Environment
	owner               Ownership
	remoteURL           string
	sandboxKey          string
	cancel              context.CancelFunc
	timer               *time.Timer
	cleanupDone         bool
	cleanup             *cleanupAttempt
	retiring            bool
	fenceStartupTimeout func(context.Context) error
}

type cleanupAttempt struct {
	done chan struct{}
	err  error
}

func New(backend Backend) *Manager {
	return &Manager{backend: backend, startupTimeout: defaultStartupTimeout, bindings: make(map[string]*binding)}
}

func (m *Manager) Provision(ctx context.Context, req ProvisionRequest) (*Environment, error) {
	if err := validateProvision(req); err != nil {
		return nil, err
	}
	if req.WorkspaceDirectory == "" {
		req.WorkspaceDirectory = WorkspaceDirectory
	}
	operationCtx, finishOperation := lifetimeOperationContext(ctx, req.Lifetime)
	defer finishOperation()
	m.mu.RLock()
	existing := m.bindings[req.LocalSessionID]
	if existing != nil {
		if !sameIdentity(existing, req.Ownership, req.UpstreamEnvironmentID, req.RemoteURL) {
			m.mu.RUnlock()
			return nil, errors.New("local session is already bound to another environment")
		}
		env := copyEnvironment(existing.environment)
		m.mu.RUnlock()
		return env, nil
	}
	m.mu.RUnlock()

	sandboxKey := sandboxSessionKey(req.LocalSessionID)
	sandboxConfig := sandbox.DefaultSandboxConfig()
	sandboxConfig.Enabled = true
	sandboxConfig.Image = req.Image
	sandboxConfig.NetworkMode = string(sandbox.NetworkWhitelist)
	sandboxConfig.AllowedHosts = append([]string(nil), requiredOpenAIHosts...)
	sandboxConfig.WorkDir = WorkspaceDirectory
	// Agent sessions are durable and may outlive the generic five-minute
	// sandbox default. Their lifecycle is owned by the session supervisor.
	sandboxConfig.TimeoutSeconds = 0
	sandboxConfig.Name = "OpenAI agent " + shortID(req.LocalSessionID)
	sandboxConfig.Labels = map[string]string{
		"everstack.ai/workload-kind": "openai-agents-api-executor",
	}
	inst, err := m.backend.Provision(operationCtx, req.Ownership, sandboxKey, sandboxConfig)
	if inst == nil {
		if err != nil {
			return nil, redactError(err, req.ExecutorAPIKey)
		}
		return nil, errors.New("sandbox provision returned no instance")
	}
	cleanup := true
	defer func() {
		if cleanup {
			cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = m.backend.Destroy(cleanCtx, req.Ownership, inst.ID, sandboxKey)
		}
	}()
	compensate := func(cause error) (*Environment, error) {
		cause = redactError(cause, req.ExecutorAPIKey)
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cleanup = false
		if cleanupErr := m.backend.Destroy(cleanCtx, req.Ownership, inst.ID, sandboxKey); cleanupErr != nil {
			combined := errors.Join(cause, fmt.Errorf("destroy sandbox after failed provision: %w", redactError(cleanupErr, req.ExecutorAPIKey)))
			return &Environment{
				SandboxID: inst.ID, LocalSessionID: req.LocalSessionID,
				UpstreamEnvironmentID: req.UpstreamEnvironmentID,
				Status:                StatusFailed, Error: sanitizeMessage(combined.Error()),
			}, combined
		}
		return nil, cause
	}
	if err != nil {
		return compensate(err)
	}

	if err := m.backend.VerifyOwnership(operationCtx, req.Ownership, inst.ID); err != nil {
		return compensate(err)
	}
	if err := m.verifyExecutor(operationCtx, req.Ownership, inst.ID, req.ExecutorAPIKey); err != nil {
		return compensate(err)
	}
	if req.Lifetime != nil && req.Lifetime.Err() != nil {
		return compensate(req.Lifetime.Err())
	}

	env := Environment{
		SandboxID: inst.ID, LocalSessionID: req.LocalSessionID,
		UpstreamEnvironmentID: req.UpstreamEnvironmentID,
		Status:                StatusPending, ExecutorVersion: ExecutorVersion,
	}
	processParent := context.Background()
	if req.Lifetime != nil {
		processParent = req.Lifetime
	}
	processCtx, cancel := context.WithCancel(processParent)
	b := &binding{environment: env, owner: req.Ownership, remoteURL: req.RemoteURL, sandboxKey: sandboxKey, cancel: cancel, fenceStartupTimeout: req.FenceStartupTimeout}

	m.mu.Lock()
	if raced := m.bindings[req.LocalSessionID]; raced != nil {
		if !sameIdentity(raced, req.Ownership, req.UpstreamEnvironmentID, req.RemoteURL) {
			m.mu.Unlock()
			cancel()
			return compensate(errors.New("local session is already bound to another environment"))
		}
		env := copyEnvironment(raced.environment)
		m.mu.Unlock()
		cancel()
		cleanup = false
		return env, nil
	}
	m.bindings[req.LocalSessionID] = b
	b.timer = time.AfterFunc(m.startupTimeout, func() { m.failIfPending(b, "executor connection timed out") })
	m.mu.Unlock()

	cleanup = false
	go m.runExecutor(processCtx, b, req.ExecutorAPIKey)
	return copyEnvironment(env), nil
}

func (m *Manager) verifyExecutor(ctx context.Context, owner Ownership, sandboxID, key string) error {
	verifyCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	result, err := m.backend.Exec(verifyCtx, owner, sandboxID, Command{
		Argv: []string{"codex", "--version"}, WorkDir: WorkspaceDirectory,
		Timeout: 20 * time.Second, SilentLog: true,
	})
	if err != nil {
		return redactError(fmt.Errorf("verify pinned Codex executor: %w", err), key)
	}
	versionFields := strings.Fields(strings.TrimSpace(result.Stdout))
	if result.ExitCode != 0 || len(versionFields) < 2 || versionFields[len(versionFields)-1] != ExecutorVersion {
		return fmt.Errorf("sandbox image does not contain required Codex executor version %s", ExecutorVersion)
	}
	return nil
}

func (m *Manager) runExecutor(ctx context.Context, b *binding, key string) {
	result, err := m.backend.Exec(ctx, b.owner, b.environment.SandboxID, Command{
		Argv:    []string{"codex", "exec-server", "--remote", b.remoteURL, "--environment-id", b.environment.UpstreamEnvironmentID},
		WorkDir: WorkspaceDirectory, Env: map[string]string{"CODEX_API_KEY": key},
		Timeout: defaultExecTimeout, SilentLog: true,
	})
	if ctx.Err() != nil {
		return
	}
	message := "executor exited before environment shutdown"
	if err != nil {
		message = redactError(err, key).Error()
	} else if result != nil {
		message = fmt.Sprintf("executor exited with code %d", result.ExitCode)
	}
	m.setFailed(b, message)
}

func (m *Manager) Observe(_ context.Context, req ObserveRequest) (*Environment, error) {
	if req.Status != StatusPending && req.Status != StatusConnected && req.Status != StatusFailed {
		return nil, errors.New("unsupported upstream environment status")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.bindings[req.LocalSessionID]
	if !ok {
		return nil, ErrNotBound
	}
	if b.owner != normalizeOwner(req.Ownership) {
		return nil, errors.New("environment is not owned by authenticated instance")
	}
	if b.retiring {
		return nil, errors.New("environment cleanup is in progress")
	}
	if b.environment.Status == StatusStopping || b.environment.Status == StatusStopped || b.environment.Status == StatusDeleting || b.environment.Status == StatusDeleted {
		return nil, errors.New("environment is stopping or deleted")
	}
	b.environment.Status = req.Status
	b.environment.Error = sanitizeMessage(req.Error)
	if req.Status == StatusConnected || req.Status == StatusFailed {
		stopTimer(b)
	}
	return copyEnvironment(b.environment), nil
}

func (m *Manager) Status(ctx context.Context, owner Ownership, localSessionID string) (*Environment, error) {
	m.mu.RLock()
	b := m.bindings[localSessionID]
	if b == nil || b.owner != normalizeOwner(owner) {
		m.mu.RUnlock()
		return nil, errors.New("environment is not owned by authenticated instance")
	}
	boundOwner, sandboxID := b.owner, b.environment.SandboxID
	env := copyEnvironment(b.environment)
	cleanupDone := b.cleanupDone
	m.mu.RUnlock()
	if env.Status == StatusStopped || env.Status == StatusDeleted || cleanupDone {
		return env, nil
	}
	if err := m.backend.VerifyOwnership(ctx, boundOwner, sandboxID); err != nil {
		return nil, err
	}
	return env, nil
}

func (m *Manager) Stop(ctx context.Context, owner Ownership, localSessionID string) error {
	return m.stop(ctx, owner, localSessionID, false)
}

func (m *Manager) Delete(ctx context.Context, owner Ownership, localSessionID string) error {
	return m.stop(ctx, owner, localSessionID, true)
}

func (m *Manager) stop(ctx context.Context, owner Ownership, localSessionID string, deleting bool) error {
	owner = normalizeOwner(owner)
	m.mu.Lock()
	b := m.bindings[localSessionID]
	if b == nil {
		m.mu.Unlock()
		return nil
	}
	if b.owner != owner {
		m.mu.Unlock()
		return errors.New("environment is not owned by authenticated instance")
	}
	if b.environment.Status == StatusDeleted || (!deleting && b.environment.Status == StatusStopped) {
		m.mu.Unlock()
		return nil
	}
	if deleting && b.environment.Status == StatusStopped {
		b.environment.Status = StatusDeleted
		m.mu.Unlock()
		return nil
	}
	if deleting {
		b.environment.Status = StatusDeleting
	} else {
		b.environment.Status = StatusStopping
	}
	b.retiring = true
	stopTimer(b)
	b.cancel()
	m.mu.Unlock()

	if err := m.cleanup(ctx, b); err != nil {
		return err
	}
	m.mu.Lock()
	if deleting {
		b.environment.Status = StatusDeleted
	} else {
		b.environment.Status = StatusStopped
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) Reconcile(ctx context.Context, req ReconcileRequest) (*Environment, error) {
	if req.Status == StatusDeleted {
		return copyEnvironment(req.Environment), nil
	}
	if req.ExecutorAPIKey == "" {
		return nil, errors.New("fresh restricted executor key is required for reconciliation")
	}
	if err := validateOwner(req.Ownership); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.LocalSessionID) == "" || strings.TrimSpace(req.UpstreamEnvironmentID) == "" || strings.TrimSpace(req.SandboxID) == "" {
		return nil, errors.New("persisted executor binding is incomplete")
	}
	if err := validateRemoteURL(req.RemoteURL); err != nil {
		return nil, err
	}
	operationCtx, finishOperation := lifetimeOperationContext(ctx, req.Lifetime)
	defer finishOperation()
	if err := m.backend.VerifyOwnership(operationCtx, req.Ownership, req.SandboxID); err != nil {
		return nil, err
	}
	if err := m.verifyExecutor(operationCtx, req.Ownership, req.SandboxID, req.ExecutorAPIKey); err != nil {
		return nil, err
	}
	if req.Lifetime != nil && req.Lifetime.Err() != nil {
		return nil, req.Lifetime.Err()
	}

	processParent := context.Background()
	if req.Lifetime != nil {
		processParent = req.Lifetime
	}
	processCtx, cancel := context.WithCancel(processParent)
	b := &binding{environment: req.Environment, owner: normalizeOwner(req.Ownership), remoteURL: req.RemoteURL, sandboxKey: sandboxSessionKey(req.LocalSessionID), cancel: cancel, fenceStartupTimeout: req.FenceStartupTimeout}
	b.environment.Status = StatusPending
	b.environment.Error = ""
	m.mu.Lock()
	if old := m.bindings[req.LocalSessionID]; old != nil {
		if old.retiring {
			m.mu.Unlock()
			cancel()
			return nil, errors.New("environment cleanup is in progress")
		}
		stopTimer(old)
		old.cancel()
	}
	m.bindings[req.LocalSessionID] = b
	b.timer = time.AfterFunc(m.startupTimeout, func() { m.failIfPending(b, "executor connection timed out") })
	m.mu.Unlock()
	env := copyEnvironment(b.environment)
	go m.runExecutor(processCtx, b, req.ExecutorAPIKey)
	return env, nil
}

func lifetimeOperationContext(ctx, lifetime context.Context) (context.Context, context.CancelFunc) {
	operationCtx, cancel := context.WithCancel(ctx)
	if lifetime == nil {
		return operationCtx, cancel
	}
	stop := context.AfterFunc(lifetime, cancel)
	return operationCtx, func() { stop(); cancel() }
}

// ResolveWorkspacePath returns an absolute path confined to /workspace. It is
// the lexical first gate; ManagerBackend performs a guest realpath check before
// every filesystem operation to reject symlink escapes as well.
func ResolveWorkspacePath(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("workspace path is required")
	}
	if strings.IndexByte(name, 0) >= 0 {
		return "", errors.New("workspace path contains NUL")
	}
	var candidate string
	if strings.HasPrefix(name, "/") {
		candidate = path.Clean(name)
	} else {
		candidate = path.Join(WorkspaceDirectory, name)
	}
	if candidate != WorkspaceDirectory && !strings.HasPrefix(candidate, WorkspaceDirectory+"/") {
		return "", errors.New("workspace path escapes /workspace")
	}
	return candidate, nil
}

func (m *Manager) failIfPending(b *binding, message string) {
	m.mu.Lock()
	if m.bindings[b.environment.LocalSessionID] == b && b.environment.Status == StatusPending {
		b.environment.Status = StatusFailed
		b.environment.Error = sanitizeMessage(message)
		b.retiring = true
		b.cancel()
		m.mu.Unlock()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if b.fenceStartupTimeout == nil || b.fenceStartupTimeout(ctx) != nil {
				return
			}
			_ = m.cleanup(ctx, b)
		}()
		return
	}
	m.mu.Unlock()
}

// cleanup serializes sandbox deletion across timeout, stop, and delete. A
// successful deletion is remembered because the backend can no longer verify
// ownership once the sandbox is gone. Failed attempts remain retryable.
func (m *Manager) cleanup(ctx context.Context, b *binding) error {
	m.mu.Lock()
	if b.cleanupDone {
		m.mu.Unlock()
		return nil
	}
	if attempt := b.cleanup; attempt != nil {
		m.mu.Unlock()
		select {
		case <-attempt.done:
			return attempt.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	attempt := &cleanupAttempt{done: make(chan struct{})}
	b.cleanup = attempt
	m.mu.Unlock()

	err := m.backend.VerifyOwnership(ctx, b.owner, b.environment.SandboxID)
	if err == nil {
		err = m.backend.Destroy(ctx, b.owner, b.environment.SandboxID, b.sandboxKey)
	}

	m.mu.Lock()
	attempt.err = err
	b.cleanupDone = err == nil
	b.cleanup = nil
	close(attempt.done)
	m.mu.Unlock()
	return err
}
func (m *Manager) setFailed(b *binding, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.bindings[b.environment.LocalSessionID] == b && b.environment.Status != StatusStopping && b.environment.Status != StatusStopped && b.environment.Status != StatusDeleting && b.environment.Status != StatusDeleted {
		b.environment.Status = StatusFailed
		b.environment.Error = sanitizeMessage(message)
		stopTimer(b)
	}
}
func stopTimer(b *binding) {
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
}
func copyEnvironment(env Environment) *Environment { copied := env; return &copied }
func normalizeOwner(o Ownership) Ownership {
	o.OrganizationID = strings.TrimSpace(o.OrganizationID)
	o.InstanceID = strings.TrimSpace(o.InstanceID)
	return o
}
func validateOwner(o Ownership) error {
	o = normalizeOwner(o)
	if o.OrganizationID == "" || o.InstanceID == "" {
		return errors.New("authenticated organization and instance are required")
	}
	return nil
}
func validateProvision(r ProvisionRequest) error {
	if err := validateOwner(r.Ownership); err != nil {
		return err
	}
	if strings.TrimSpace(r.LocalSessionID) == "" || strings.TrimSpace(r.UpstreamEnvironmentID) == "" {
		return errors.New("local session and upstream environment are required")
	}
	if r.ExecutorAPIKey == "" {
		return errors.New("restricted executor API key is required")
	}
	if strings.TrimSpace(r.Image) == "" {
		return errors.New("verified executor image is required")
	}
	if r.WorkspaceDirectory != "" && path.Clean(r.WorkspaceDirectory) != WorkspaceDirectory {
		return errors.New("OpenAI executor workspace must be /workspace")
	}
	return validateRemoteURL(r.RemoteURL)
}
func validateRemoteURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "wss") || u.User != nil {
		return errors.New("invalid upstream environment remote URL")
	}
	return nil
}
func sameIdentity(b *binding, o Ownership, upstreamID, remote string) bool {
	return b.owner == normalizeOwner(o) && b.environment.UpstreamEnvironmentID == upstreamID && b.remoteURL == remote
}
func sandboxSessionKey(id string) string {
	sum := sha256.Sum256([]byte("openai-agents-api\x00" + id))
	return "openai_" + hex.EncodeToString(sum[:16])
}
func shortID(id string) string { sum := sha256.Sum256([]byte(id)); return hex.EncodeToString(sum[:4]) }
func sanitizeMessage(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 512 {
		s = s[:512]
	}
	return s
}
func redactError(err error, secret string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if secret != "" {
		msg = strings.ReplaceAll(msg, secret, "[REDACTED]")
	}
	return errors.New(sanitizeMessage(msg))
}
