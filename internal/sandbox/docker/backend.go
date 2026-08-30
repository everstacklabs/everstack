// Package docker provides a Docker-based sandbox backend for long-lived containers.
package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	isolationdocker "github.com/everstacklabs/everstack/internal/functions/isolation/docker"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/sandbox/egress"
	"github.com/everstacklabs/everstack/internal/sandbox/logbuffer"
)

// DockerBackend implements sandbox.Backend using long-lived Docker containers.
// Unlike the isolation Docker backend which creates ephemeral containers per call,
// this backend keeps containers running across multiple Exec/WriteFile/ReadFile operations.
type DockerBackend struct {
	client      *client.Client
	config      DockerConfig
	imageMu     sync.Mutex // protects concurrent image builds
	logsMu      sync.RWMutex
	logs        map[string]*logbuffer.Buffer // sandbox ID → log buffer
	proxyMu     sync.Mutex
	portProxies map[string]map[int]*portProxy // sandbox ID → port → proxy
	egress      egress.Controller             // optional egress sidecar controller
}

// DockerConfig holds configuration for the Docker sandbox backend.
type DockerConfig struct {
	Host        string // Docker host (auto-detect if empty)
	ImagePrefix string // e.g., "ghcr.io/everstacklabs/sandbox"
	LabelPrefix string // e.g., "everstack.sandbox"
	AutoPull    bool
	AutoBuild   bool // Build from embedded Dockerfile if pull fails
}

// DefaultDockerConfig returns sensible defaults.
func DefaultDockerConfig() DockerConfig {
	return DockerConfig{
		ImagePrefix: sandbox.DefaultImageBase,
		LabelPrefix: "everstack.sandbox",
		AutoPull:    true,
		AutoBuild:   true,
	}
}

// New creates a new Docker sandbox backend.
// Reuses Docker socket detection from the existing isolation package.
func New(cfg DockerConfig) (*DockerBackend, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	host := cfg.Host
	if host == "" {
		// Auto-detect Docker socket using the shared utility from isolation/docker
		paths := isolationdocker.CommonSocketPaths()
		for _, p := range paths {
			socketHost := "unix://" + p
			opts := []client.Opt{
				client.WithHost(socketHost),
				client.WithAPIVersionNegotiation(),
			}
			testCli, err := client.NewClientWithOpts(opts...)
			if err != nil {
				continue
			}
			if _, err := testCli.Ping(ctx); err != nil {
				testCli.Close()
				continue
			}
			testCli.Close()
			host = socketHost
			break
		}
		if host == "" {
			return nil, fmt.Errorf("docker sandbox: could not detect Docker socket")
		}
	}

	opts := []client.Opt{
		client.WithHost(host),
		client.WithAPIVersionNegotiation(),
	}
	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("docker sandbox: failed to create client: %w", err)
	}

	if _, err := cli.Ping(ctx); err != nil {
		cli.Close()
		return nil, fmt.Errorf("docker sandbox: failed to connect to Docker at %s: %w", host, err)
	}

	logger.WithFields("host", host).Info("docker_sandbox: connected to Docker")

	return &DockerBackend{client: cli, config: cfg, logs: make(map[string]*logbuffer.Buffer)}, nil
}

func (b *DockerBackend) Name() string { return "docker" }

func (b *DockerBackend) RunnerCapabilities() sandbox.RunnerCapabilities {
	return sandbox.RunnerCapabilities{
		Target:    b.Name(),
		Placement: sandbox.RunnerPlacementLocal,
		Health:    sandbox.RunnerHealthDockerDaemon,
		Features: sandbox.RunnerFeatures{
			WorkspaceSnapshot: true,
			DockerCPSnapshot:  true,
			GitImport:         true,
			PortExposure:      true,
			PortDetection:     true,
			ComputerUse:       true,
		},
	}
}

// SetEgressController sets the egress sidecar controller.
// When set, whitelist-mode sandboxes get a DNS-proxy sidecar for egress enforcement.
func (b *DockerBackend) SetEgressController(ctrl egress.Controller) {
	b.egress = ctrl
}

// getOrCreateLogs returns the log buffer for a sandbox, creating one if needed.
func (b *DockerBackend) getOrCreateLogs(id string) *logbuffer.Buffer {
	b.logsMu.RLock()
	sl, ok := b.logs[id]
	b.logsMu.RUnlock()
	if ok {
		return sl
	}

	b.logsMu.Lock()
	defer b.logsMu.Unlock()
	if sl, ok := b.logs[id]; ok {
		return sl
	}
	sl = logbuffer.NewBuffer()
	b.logs[id] = sl
	return sl
}

// Create provisions a new long-lived Docker container for the sandbox.
func (b *DockerBackend) Create(ctx context.Context, id string, config sandbox.InstanceConfig) (*sandbox.Instance, error) {
	imageName := config.Image
	if imageName == "" {
		imageName = b.config.ImagePrefix + ":base"
	}

	// Pull image if needed
	if b.config.AutoPull {
		if err := b.ensureImage(ctx, imageName); err != nil {
			return nil, fmt.Errorf("failed to ensure image %s: %w", imageName, err)
		}
	}

	// Build environment variables
	workDir := config.WorkDir
	if workDir == "" {
		workDir = "/workspace"
	}

	// Set HOME to the writable trooper so the shell can write history/profile
	// files without needing a tmpfs on /home/ubuntu.
	// Set TMPDIR to a trooper subdirectory so tools that respect it use the
	// larger trooper tmpfs instead of the small /tmp (256MB vs trooper size).
	//
	// Package manager configuration:
	//   NPM_CONFIG_PREFIX  — redirects global installs to writable trooper
	//   NPM_CONFIG_CACHE   — explicit cache location on writable tmpfs
	//   PIP_TARGET         — pip install --target equivalent
	//   CARGO_HOME         — cargo/rustup home directory
	//   GOPATH             — Go module cache
	//   PATH               — prepend trooper bin dirs so globally installed tools are found
	envSlice := []string{
		"HOME=" + workDir,
		"TMPDIR=/workspace/.tmp",
		"NPM_CONFIG_PREFIX=/workspace/.npm-global",
		"NPM_CONFIG_CACHE=/workspace/.npm-cache",
		"PIP_TARGET=/workspace/.pip-packages",
		"CARGO_HOME=/workspace/.cargo",
		"GOPATH=/workspace/.go",
		"PATH=/workspace/.npm-global/bin:/workspace/.pip-packages/bin:/workspace/.cargo/bin:/workspace/.go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	for k, v := range config.EnvVars {
		envSlice = append(envSlice, fmt.Sprintf("%s=%s", k, v))
	}

	expiresAt := time.Now().Add(time.Duration(config.TimeoutSeconds) * time.Second)
	now := time.Now()

	// Init command creates package-manager directories before sleeping.
	// The trooper tmpfs starts empty, so npm/pip/cargo need their target
	// directories created up-front to avoid failures on first use.
	// If a repo is bind-mounted at /repo, create a symlink /workspace/repo → /repo for convenience.
	repoSymlink := ""
	if config.RepoHostPath != "" {
		repoSymlink = " && ln -sf /repo " + workDir + "/repo"
	}
	initCmd := fmt.Sprintf(
		"mkdir -p %[1]s/.npm-global/bin %[1]s/.npm-cache %[1]s/.tmp %[1]s/.pip-packages/bin %[1]s/.cargo/bin %[1]s/.go/bin%[2]s && exec sleep infinity",
		workDir, repoSymlink,
	)

	containerConfig := &container.Config{
		Image:      imageName,
		Cmd:        []string{"sh", "-c", initCmd}, // Create dirs then keep alive
		WorkingDir: workDir,
		Env:        envSlice,
		// Run as UID/GID 1000 to match tmpfs ownership. Without CAP_DAC_OVERRIDE
		// (dropped below), root cannot write to UID 1000-owned tmpfs mounts.
		User: "1000:1000",
		Labels: map[string]string{
			b.config.LabelPrefix + ".id":             id,
			b.config.LabelPrefix + ".session_id":     config.SessionID,
			b.config.LabelPrefix + ".tenant_id":      config.TenantID,
			b.config.LabelPrefix + ".name":           config.Name,
			b.config.LabelPrefix + ".expires_at":     expiresAt.Format(time.RFC3339),
			b.config.LabelPrefix + ".last_used_at":   now.Format(time.RFC3339),
			b.config.LabelPrefix + ".idle_retention": fmt.Sprintf("%d", config.TimeoutSeconds),
			b.config.LabelPrefix + ".persistent":     fmt.Sprintf("%t", config.AgentID != ""),
			b.config.LabelPrefix + ".agent_id":       config.AgentID,
		},
	}

	networkMode := "none"
	switch config.NetworkMode {
	case sandbox.NetworkAllow:
		networkMode = "bridge"
	case sandbox.NetworkWhitelist:
		networkMode = "bridge" // iptables rules would be applied separately
	case sandbox.NetworkDeny:
		networkMode = "none"
	}

	networkMode = b.resolveContainerNetworkMode(ctx, networkMode)

	hostConfig := &container.HostConfig{
		Resources: container.Resources{
			NanoCPUs: int64(config.CPULimit * 1e9),
			Memory:   config.MemoryMB * 1024 * 1024,
		},
		SecurityOpt:    []string{"no-new-privileges"},
		CapDrop:        []string{"ALL"},
		ReadonlyRootfs: true,
		Tmpfs: map[string]string{
			"/tmp":  fmt.Sprintf("exec,rw,nosuid,size=%dm,uid=1000,gid=1000", 256),           // explicit exec: npm/pip lifecycle scripts execute from /tmp
			workDir: fmt.Sprintf("exec,rw,nosuid,size=%dm,uid=1000,gid=1000", config.DiskMB), // explicit exec: npx/pip/cargo installed binaries need +x
			"/run":  "rw,noexec,nosuid,size=10m,uid=1000,gid=1000",
		},
		NetworkMode: container.NetworkMode(networkMode),
	}

	// If a git repo was cloned to the host, bind-mount it read-only at /repo.
	// Kernel-enforced read-only — writes from inside container return EROFS.
	if config.RepoHostPath != "" {
		hostConfig.Mounts = append(hostConfig.Mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   config.RepoHostPath,
			Target:   "/repo",
			ReadOnly: true,
		})
	}

	// Configure explicit DNS servers when the container has network access.
	// Docker's embedded DNS (127.0.0.11) silently fails in common environments
	// (OrbStack, Docker-in-Docker, Colima), so we set public resolvers explicitly.
	if networkMode != "none" && len(config.DNSServers) > 0 {
		hostConfig.DNS = config.DNSServers
	}

	resp, err := b.client.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, id)
	if err != nil {
		// If a stale container with the same name exists (e.g. from a previous
		// server run), remove it and retry once.
		if strings.Contains(err.Error(), "Conflict") || strings.Contains(err.Error(), "already in use") {
			logger.WithFields("sandbox_id", id).
				Info("docker_sandbox: removing stale container before retry")
			_ = b.client.ContainerRemove(ctx, id, container.RemoveOptions{Force: true})
			resp, err = b.client.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, id)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to create container: %w", err)
		}
	}

	if err := b.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		// Clean up created container
		_ = b.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	logger.WithFields("sandbox_id", id, "container_id", resp.ID[:12], "image", imageName).
		Info("docker_sandbox: container created and started")

	// Start egress sidecar for whitelist mode. Whitelist is a restricted-network
	// request; fail closed if enforcement cannot start instead of silently leaving
	// the container on unrestricted bridge.
	if err := b.startEgressControl(ctx, id, config); err != nil {
		_ = b.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return nil, err
	}

	// Initialize the application-level log buffer for this sandbox
	sl := b.getOrCreateLogs(id)
	sl.Append(logbuffer.Entry{
		Timestamp: now,
		Stream:    "system",
		Line:      fmt.Sprintf("sandbox created (image=%s)", imageName),
	})

	return &sandbox.Instance{
		ID:          id,
		ContainerID: resp.ID,
		Status:      sandbox.StatusRunning,
		Config:      config,
		CreatedAt:   now,
		ExpiresAt:   expiresAt,
		LastUsedAt:  now,
		Backend:     "docker",
	}, nil
}

func (b *DockerBackend) startEgressControl(ctx context.Context, id string, config sandbox.InstanceConfig) error {
	if config.NetworkMode != sandbox.NetworkWhitelist {
		return nil
	}
	if b.egress == nil {
		return fmt.Errorf("docker whitelist egress controller not configured")
	}
	if err := b.egress.Start(ctx, id, egress.EgressConfig{
		Mode:         egress.EgressWhitelist,
		AllowedHosts: config.AllowedHosts,
		DNSServers:   config.DNSServers,
	}); err != nil {
		return fmt.Errorf("docker whitelist egress enforcement: %w", err)
	}
	return nil
}

// resolveContainerNetworkMode picks the Docker network mode for new sandbox containers.
//
// Why this exists:
//   - When the gateway runs inside Docker, forcing sandbox containers onto the
//     daemon's default "bridge" can make them unreachable from the gateway
//     container (different subnets / no route).
//   - In that setup, running sandboxes on the gateway container's network makes
//     container-to-container routing work for exposed ports.
//
// Override: EVS_SANDBOX_DOCKER_NETWORK takes precedence when set.
func (b *DockerBackend) resolveContainerNetworkMode(ctx context.Context, requested string) string {
	if requested == "none" {
		return "none"
	}

	if forced := strings.TrimSpace(os.Getenv("EVS_SANDBOX_DOCKER_NETWORK")); forced != "" {
		logger.WithFields("network_mode", forced).
			Info("docker_sandbox: using network mode from EVS_SANDBOX_DOCKER_NETWORK")
		return forced
	}

	// Best-effort autodetect when gateway itself runs in Docker.
	hostname := strings.TrimSpace(os.Getenv("HOSTNAME"))
	if hostname == "" {
		return requested
	}

	inspectCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	self, err := b.client.ContainerInspect(inspectCtx, hostname)
	if err != nil || self.NetworkSettings == nil || len(self.NetworkSettings.Networks) == 0 {
		return requested
	}

	networkNames := make([]string, 0, len(self.NetworkSettings.Networks))
	for name := range self.NetworkSettings.Networks {
		if name == "" || name == "none" || name == "host" {
			continue
		}
		networkNames = append(networkNames, name)
	}
	if len(networkNames) == 0 {
		return requested
	}
	sort.Strings(networkNames)

	// Prefer a non-default bridge network (common in docker-compose), then fallback.
	for _, name := range networkNames {
		if name != "bridge" {
			logger.WithFields("network_mode", name, "requested", requested).
				Info("docker_sandbox: using gateway container network mode")
			return name
		}
	}

	logger.WithFields("network_mode", networkNames[0], "requested", requested).
		Info("docker_sandbox: using detected network mode")
	return networkNames[0]
}

// isContainerGone returns true if the error indicates the Docker container
// has been removed or is not running. This is used to distinguish between
// transient errors (retryable) and permanent sandbox death (needs recreation).
func isContainerGone(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such container") ||
		strings.Contains(msg, "is not running") ||
		strings.Contains(msg, "container not found") ||
		strings.Contains(msg, "is restarting") ||
		strings.Contains(msg, "removal of container") ||
		strings.Contains(msg, "is dead")
}

// Exec runs a command inside the running container.
func (b *DockerBackend) Exec(ctx context.Context, id string, req sandbox.ExecRequest) (*sandbox.ExecResult, error) {
	start := time.Now()

	timeout := req.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	// Use a separate context for exec creation/attach so we can control the
	// timeout precisely. The parent ctx is still respected for cancellation.
	execCtx, execCancel := context.WithTimeout(ctx, timeout)
	defer execCancel()

	workDir := req.WorkDir
	if workDir == "" {
		workDir = "/workspace"
	}

	var envSlice []string
	for k, v := range req.Env {
		envSlice = append(envSlice, fmt.Sprintf("%s=%s", k, v))
	}

	execConfig := container.ExecOptions{
		Cmd:          req.Command,
		WorkingDir:   workDir,
		AttachStdout: true,
		AttachStderr: true,
		Env:          envSlice,
	}

	execResp, err := b.client.ContainerExecCreate(execCtx, id, execConfig)
	if err != nil {
		if isContainerGone(err) {
			return nil, fmt.Errorf("%w: %v", sandbox.ErrSandboxNotRunning, err)
		}
		return nil, fmt.Errorf("failed to create exec: %w", err)
	}

	attachResp, err := b.client.ContainerExecAttach(execCtx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		if isContainerGone(err) {
			return nil, fmt.Errorf("%w: %v", sandbox.ErrSandboxNotRunning, err)
		}
		return nil, fmt.Errorf("failed to attach exec: %w", err)
	}

	// Read stdout/stderr in a goroutine so we can enforce the timeout.
	// stdcopy.StdCopy blocks on the Docker multiplexed stream and does not
	// respect Go contexts, so we need to close the connection to unblock it.
	var stdout, stderr bytes.Buffer
	copyDone := make(chan error, 1)
	go func() {
		_, err := stdcopy.StdCopy(&stdout, &stderr, attachResp.Reader)
		copyDone <- err
	}()

	var timedOut bool
	select {
	case err = <-copyDone:
		// Normal completion
		if err != nil && err != io.EOF {
			if execCtx.Err() != nil {
				timedOut = true
			} else {
				attachResp.Close()
				return nil, fmt.Errorf("failed to read exec output: %w", err)
			}
		}
	case <-execCtx.Done():
		// Timeout — close the attach connection to unblock StdCopy
		timedOut = true
		attachResp.Close()
		<-copyDone // wait for goroutine to finish
	}

	if !timedOut {
		attachResp.Close()
	}

	// Use a fresh context for inspect — execCtx may be cancelled after timeout
	inspectCtx, inspectCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer inspectCancel()

	// Capture exec output into the application log buffer, unless this is an
	// internal introspection exec (SilentLog) whose plumbing would just be
	// noise in the user-facing Logs tab.
	if !req.SilentLog {
		sl := b.getOrCreateLogs(id)
		now := time.Now()
		cmdStr := strings.Join(req.Command, " ")
		sl.Append(logbuffer.Entry{Timestamp: now, Stream: "system", Line: fmt.Sprintf("$ %s", cmdStr)})
		if s := stdout.String(); s != "" {
			for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
				sl.Append(logbuffer.Entry{Timestamp: now, Stream: "stdout", Line: line})
			}
		}
		if s := stderr.String(); s != "" {
			for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
				sl.Append(logbuffer.Entry{Timestamp: now, Stream: "stderr", Line: line})
			}
		}
	}

	if timedOut {
		return &sandbox.ExecResult{
			ExitCode:   -1,
			Stdout:     truncateOutput(stdout.String()),
			Stderr:     truncateOutput(stderr.String()),
			DurationMs: time.Since(start).Milliseconds(),
			TimedOut:   true,
		}, nil
	}

	// Get exit code with a fresh context
	inspectResp, err := b.client.ContainerExecInspect(inspectCtx, execResp.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect exec: %w", err)
	}

	return &sandbox.ExecResult{
		ExitCode:   inspectResp.ExitCode,
		Stdout:     truncateOutput(stdout.String()),
		Stderr:     truncateOutput(stderr.String()),
		DurationMs: time.Since(start).Milliseconds(),
		TimedOut:   false,
	}, nil
}

// WriteFile writes content to a path inside the container using exec.
// We use exec+tee instead of CopyToContainer because the Docker copy API
// doesn't work reliably with read-only rootfs + tmpfs mounts.
func (b *DockerBackend) WriteFile(ctx context.Context, id string, path string, content []byte) error {
	// Use base64 encoding to safely pass binary content through shell
	encoded := base64.StdEncoding.EncodeToString(content)

	// mkdir -p ensures parent directories exist — the writable tmpfs starts
	// empty so any nested path like /workspace/a/b/file.txt would fail without it.
	dir := filepath.Dir(path)
	execConfig := container.ExecOptions{
		Cmd:          []string{"sh", "-c", fmt.Sprintf("mkdir -p '%s' && echo '%s' | base64 -d > '%s'", dir, encoded, path)},
		AttachStdout: true,
		AttachStderr: true,
	}

	execResp, err := b.client.ContainerExecCreate(ctx, id, execConfig)
	if err != nil {
		if isContainerGone(err) {
			return fmt.Errorf("%w: %v", sandbox.ErrSandboxNotRunning, err)
		}
		return fmt.Errorf("failed to create exec: %w", err)
	}

	attachResp, err := b.client.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		if isContainerGone(err) {
			return fmt.Errorf("%w: %v", sandbox.ErrSandboxNotRunning, err)
		}
		return fmt.Errorf("failed to attach exec: %w", err)
	}
	defer attachResp.Close()

	// Read output to completion
	_, _ = io.Copy(io.Discard, attachResp.Reader)

	// Check exit code
	inspectResp, err := b.client.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		if isContainerGone(err) {
			return fmt.Errorf("%w: %v", sandbox.ErrSandboxNotRunning, err)
		}
		return fmt.Errorf("failed to inspect exec: %w", err)
	}
	if inspectResp.ExitCode != 0 {
		return fmt.Errorf("write failed with exit code %d", inspectResp.ExitCode)
	}

	return nil
}

// ReadFile reads content from a path inside the container.
//
// We intentionally use exec+head/cat instead of Docker CopyFromContainer here.
// In long-lived sandbox sessions, CopyFromContainer can intermittently return
// "file not found" for existing files (racey path resolution/overlay behavior),
// while an in-container read is deterministic.
func (b *DockerBackend) ReadFile(ctx context.Context, id string, path string) ([]byte, error) {
	const maxSize = 1 << 20 // 1MB
	escapedPath := strings.ReplaceAll(path, "'", `'"'"'`)
	cmd := fmt.Sprintf(
		"if [ -f '%s' ]; then head -c %d '%s'; else exit 42; fi",
		escapedPath, maxSize, escapedPath,
	)

	result, err := b.Exec(ctx, id, sandbox.ExecRequest{
		Command: []string{"sh", "-c", cmd},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	if result.ExitCode == 42 {
		return nil, fmt.Errorf("file not found: %s", path)
	}
	if result.ExitCode != 0 {
		msg := strings.TrimSpace(result.Stderr)
		if msg == "" {
			msg = "unknown error"
		}
		return nil, fmt.Errorf("failed to read file %s: %s", path, msg)
	}
	return []byte(result.Stdout), nil
}

// ListFiles lists directory contents inside the container.
func (b *DockerBackend) ListFiles(ctx context.Context, id string, path string) ([]sandbox.FileInfo, error) {
	// Use exec to run ls -la
	result, err := b.Exec(ctx, id, sandbox.ExecRequest{
		Command: []string{"find", path, "-maxdepth", "1", "-printf", "%y %s %p\\n"},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list files: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("ls failed: %s", result.Stderr)
	}

	var files []sandbox.FileInfo
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 3)
		if len(parts) != 3 {
			continue
		}
		fileType := parts[0]
		filePath := parts[2]
		if filePath == path {
			continue // Skip the directory itself
		}
		var size int64
		fmt.Sscanf(parts[1], "%d", &size)
		files = append(files, sandbox.FileInfo{
			Name:  filepath.Base(filePath),
			Path:  filePath,
			Size:  size,
			IsDir: fileType == "d",
		})
	}

	return files, nil
}

// Destroy stops and removes the container.
func (b *DockerBackend) Destroy(ctx context.Context, id string) error {
	// Stop egress sidecar if running
	if b.egress != nil {
		_ = b.egress.Stop(ctx, id)
	}

	// Close all port proxies for this sandbox
	b.closeAllProxies(id)

	// Clean up the application log buffer
	b.logsMu.Lock()
	if sl, ok := b.logs[id]; ok {
		sl.Close()
		delete(b.logs, id)
	}
	b.logsMu.Unlock()

	timeout := 5
	if err := b.client.ContainerStop(ctx, id, container.StopOptions{Timeout: &timeout}); err != nil {
		logger.WithFields("sandbox_id", id, "error", err.Error()).
			Debug("docker_sandbox: stop failed, forcing remove")
	}

	if err := b.client.ContainerRemove(ctx, id, container.RemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("failed to remove container: %w", err)
	}

	logger.WithFields("sandbox_id", id).Debug("docker_sandbox: container destroyed")
	return nil
}

// Status returns the current state of a sandbox container.
func (b *DockerBackend) Status(ctx context.Context, id string) (*sandbox.Instance, error) {
	inspect, err := b.client.ContainerInspect(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}

	status := sandbox.StatusRunning
	if !inspect.State.Running {
		status = sandbox.StatusStopped
		if inspect.State.ExitCode != 0 {
			status = sandbox.StatusFailed
		}
	}

	return &sandbox.Instance{
		ID:          id,
		ContainerID: inspect.ID,
		Status:      status,
		Backend:     "docker",
	}, nil
}

func (b *DockerBackend) DescribePending(ctx context.Context, id string) string {
	return ""
}

// Healthy checks if the Docker daemon is reachable.
func (b *DockerBackend) Healthy(ctx context.Context) error {
	_, err := b.client.Ping(ctx)
	return err
}

// EnsureImage satisfies sandbox.ImageWarmer so the manager can pre-pull
// known images at startup, eliminating the 10–30s first-pull stall on the
// first sandbox create.
func (b *DockerBackend) EnsureImage(ctx context.Context, imageName string) error {
	return b.ensureImage(ctx, imageName)
}

func (b *DockerBackend) ensureImage(ctx context.Context, imageName string) error {
	// 1. Check local
	_, _, err := b.client.ImageInspectWithRaw(ctx, imageName)
	if err == nil {
		return nil
	}

	// 2. Try pull from registry
	logger.WithFields("image", imageName).Info("docker_sandbox: pulling image")
	reader, pullErr := b.client.ImagePull(ctx, imageName, image.PullOptions{})
	if pullErr == nil {
		defer reader.Close()
		_, _ = io.Copy(io.Discard, reader)
		return nil
	}
	logger.WithFields("image", imageName, "error", pullErr.Error()).
		Info("docker_sandbox: pull failed, will attempt local build")

	// 3. Build from embedded Dockerfile
	if !b.config.AutoBuild {
		return fmt.Errorf("image %s not found locally and pull failed: %w", imageName, pullErr)
	}
	return b.buildImage(ctx, imageName)
}

func (b *DockerBackend) buildImage(ctx context.Context, imageName string) error {
	b.imageMu.Lock()
	defer b.imageMu.Unlock()

	// Double-check: another goroutine may have built it while we waited
	_, _, err := b.client.ImageInspectWithRaw(ctx, imageName)
	if err == nil {
		return nil
	}

	logger.WithFields("image", imageName).
		Info("docker_sandbox: building image from embedded Dockerfile (this may take a few minutes)")

	// Create tar build context with embedded Dockerfile
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name: "Dockerfile",
		Mode: 0644,
		Size: int64(len(EmbeddedDockerfile)),
	}); err != nil {
		return fmt.Errorf("failed to create build context: %w", err)
	}
	if _, err := tw.Write(EmbeddedDockerfile); err != nil {
		return fmt.Errorf("failed to write Dockerfile to build context: %w", err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("failed to close build context: %w", err)
	}

	resp, err := b.client.ImageBuild(ctx, &buf, build.ImageBuildOptions{
		Dockerfile:  "Dockerfile",
		Tags:        []string{imageName},
		Remove:      true,
		ForceRemove: true,
	})
	if err != nil {
		return fmt.Errorf("failed to start image build: %w", err)
	}
	defer resp.Body.Close()

	// Stream build output to logs
	decoder := json.NewDecoder(resp.Body)
	for {
		var msg struct {
			Stream string `json:"stream"`
			Error  string `json:"error"`
		}
		if err := decoder.Decode(&msg); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to read build output: %w", err)
		}
		if msg.Error != "" {
			return fmt.Errorf("image build failed: %s", msg.Error)
		}
		if msg.Stream != "" {
			logger.WithFields("image", imageName).
				Debug("docker_sandbox: build: " + strings.TrimSpace(msg.Stream))
		}
	}

	logger.WithFields("image", imageName).Info("docker_sandbox: image built successfully")
	return nil
}

// Logs returns a stream of sandbox logs captured from exec and shell output.
// Docker's ContainerLogs API only captures PID 1 (sleep infinity) output, which
// is always empty. Instead we stream from the application-level log buffer.
func (b *DockerBackend) Logs(ctx context.Context, id string, opts sandbox.LogsOptions) (io.ReadCloser, error) {
	sl := b.getOrCreateLogs(id)

	pr, pw := io.Pipe()

	go func() {
		defer pw.Close()

		// Write existing entries (tail)
		entries := sl.Snapshot(opts.Tail)
		for _, e := range entries {
			if !opts.Since.IsZero() && e.Timestamp.Before(opts.Since) {
				continue
			}
			line := logbuffer.FormatEntry(e, opts.Timestamps)
			if _, err := pw.Write([]byte(line + "\n")); err != nil {
				return
			}
		}

		if !opts.Follow {
			return
		}

		// Subscribe for new entries
		ch := sl.Subscribe()
		defer sl.Unsubscribe(ch)

		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-ch:
				if !ok {
					return // log buffer closed (sandbox destroyed)
				}
				line := logbuffer.FormatEntry(e, opts.Timestamps)
				if _, err := pw.Write([]byte(line + "\n")); err != nil {
					return
				}
			}
		}
	}()

	return pr, nil
}

// Stats returns a one-shot resource usage snapshot for a container.
func (b *DockerBackend) Stats(ctx context.Context, id string) (*sandbox.ContainerStats, error) {
	resp, err := b.client.ContainerStats(ctx, id, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get container stats: %w", err)
	}
	defer resp.Body.Close()

	var raw struct {
		CPUStats struct {
			CPUUsage struct {
				TotalUsage int64 `json:"total_usage"`
			} `json:"cpu_usage"`
			SystemCPUUsage int64 `json:"system_cpu_usage"`
			OnlineCPUs     int   `json:"online_cpus"`
		} `json:"cpu_stats"`
		PreCPUStats struct {
			CPUUsage struct {
				TotalUsage int64 `json:"total_usage"`
			} `json:"cpu_usage"`
			SystemCPUUsage int64 `json:"system_cpu_usage"`
		} `json:"precpu_stats"`
		MemoryStats struct {
			Usage int64 `json:"usage"`
			Limit int64 `json:"limit"`
		} `json:"memory_stats"`
		Networks map[string]struct {
			RxBytes int64 `json:"rx_bytes"`
			TxBytes int64 `json:"tx_bytes"`
		} `json:"networks"`
		BlkioStats struct {
			IOServiceBytesRecursive []struct {
				Op    string `json:"op"`
				Value int64  `json:"value"`
			} `json:"io_service_bytes_recursive"`
		} `json:"blkio_stats"`
		PIDs struct {
			Current int `json:"current"`
		} `json:"pids_stats"`
	}

	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("failed to decode container stats: %w", err)
	}

	// Calculate CPU percentage
	cpuDelta := float64(raw.CPUStats.CPUUsage.TotalUsage - raw.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(raw.CPUStats.SystemCPUUsage - raw.PreCPUStats.SystemCPUUsage)
	var cpuPercent float64
	if systemDelta > 0 && cpuDelta > 0 {
		cpuPercent = (cpuDelta / systemDelta) * float64(raw.CPUStats.OnlineCPUs) * 100.0
	}

	// Memory
	memPercent := 0.0
	if raw.MemoryStats.Limit > 0 {
		memPercent = float64(raw.MemoryStats.Usage) / float64(raw.MemoryStats.Limit) * 100.0
	}

	// Network totals
	var rxBytes, txBytes int64
	for _, net := range raw.Networks {
		rxBytes += net.RxBytes
		txBytes += net.TxBytes
	}

	// Block I/O
	var blockRead, blockWrite int64
	for _, entry := range raw.BlkioStats.IOServiceBytesRecursive {
		switch entry.Op {
		case "read", "Read":
			blockRead += entry.Value
		case "write", "Write":
			blockWrite += entry.Value
		}
	}

	return &sandbox.ContainerStats{
		CPUPercent:     cpuPercent,
		MemoryUsage:    raw.MemoryStats.Usage,
		MemoryLimit:    raw.MemoryStats.Limit,
		MemoryPercent:  memPercent,
		NetworkRxBytes: rxBytes,
		NetworkTxBytes: txBytes,
		BlockRead:      blockRead,
		BlockWrite:     blockWrite,
		PIDs:           raw.PIDs.Current,
		Timestamp:      time.Now(),
	}, nil
}

// shellConn wraps a Docker HijackedResponse so that reads go through the
// buffered reader (which may contain data read-ahead during the HTTP upgrade)
// while writes go directly to the underlying connection.
type shellConn struct {
	reader io.Reader
	writer io.Writer
	closer io.Closer
}

func (c *shellConn) Read(p []byte) (int, error)  { return c.reader.Read(p) }
func (c *shellConn) Write(p []byte) (int, error) { return c.writer.Write(p) }
func (c *shellConn) Close() error                { return c.closer.Close() }

// Shell opens an interactive TTY shell session inside the container.
func (b *DockerBackend) Shell(ctx context.Context, id string, cmd []string) (*sandbox.ShellSession, error) {
	if len(cmd) == 0 {
		cmd = []string{"/bin/sh"}
	}

	execConfig := container.ExecOptions{
		Cmd:          cmd,
		Env:          []string{"TERM=xterm", "PS1=\033[97m◆\033[0m \033[90m$PWD\033[0m \033[90m$\033[0m "},
		Tty:          true,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	}

	execResp, err := b.client.ContainerExecCreate(ctx, id, execConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create exec for shell: %w", err)
	}

	attachResp, err := b.client.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{Tty: true})
	if err != nil {
		return nil, fmt.Errorf("failed to attach exec for shell: %w", err)
	}

	// Tee shell output into the application log buffer so it appears in the Logs tab.
	sl := b.getOrCreateLogs(id)
	sl.Append(logbuffer.Entry{Timestamp: time.Now(), Stream: "system", Line: "shell session started"})
	teeW := logbuffer.NewTeeWriter(sl)
	teeReader := io.TeeReader(attachResp.Reader, teeW)

	// Use attachResp.Reader for reads — it's a *bufio.Reader that may already
	// contain data buffered during the HTTP upgrade handshake (shell prompt, etc.).
	// Writing goes directly to the raw Conn.
	return &sandbox.ShellSession{
		Conn: &shellConn{
			reader: teeReader,
			writer: attachResp.Conn,
			closer: attachResp.Conn,
		},
		Resize: func(rows, cols uint16) error {
			return b.client.ContainerExecResize(ctx, execResp.ID, container.ResizeOptions{
				Height: uint(rows),
				Width:  uint(cols),
			})
		},
	}, nil
}

// List returns all running sandbox containers known to Docker.
// Used on startup to rediscover containers from a previous run.
func (b *DockerBackend) List(ctx context.Context) ([]*sandbox.Instance, error) {
	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	containers, err := b.client.ContainerList(listCtx, container.ListOptions{
		Filters: filters.NewArgs(
			filters.Arg("label", b.config.LabelPrefix+".id"),
			filters.Arg("status", "running"),
		),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list sandbox containers: %w", err)
	}

	var instances []*sandbox.Instance
	for _, c := range containers {
		id := c.Labels[b.config.LabelPrefix+".id"]
		sessionID := c.Labels[b.config.LabelPrefix+".session_id"]
		tenantID := c.Labels[b.config.LabelPrefix+".tenant_id"]
		name := strings.TrimSpace(c.Labels[b.config.LabelPrefix+".name"])
		expiresAtStr := c.Labels[b.config.LabelPrefix+".expires_at"]
		lastUsedAtStr := c.Labels[b.config.LabelPrefix+".last_used_at"]
		idleRetentionStr := c.Labels[b.config.LabelPrefix+".idle_retention"]
		persistentStr := c.Labels[b.config.LabelPrefix+".persistent"]
		agentID := c.Labels[b.config.LabelPrefix+".agent_id"]

		if id == "" || sessionID == "" {
			continue
		}

		var expiresAt time.Time
		if expiresAtStr != "" {
			if parsed, err := time.Parse(time.RFC3339, expiresAtStr); err == nil {
				expiresAt = parsed
			}
		}

		var lastUsedAt time.Time
		if lastUsedAtStr != "" {
			if parsed, err := time.Parse(time.RFC3339, lastUsedAtStr); err == nil {
				lastUsedAt = parsed
			}
		}

		var idleRetentionSecs int
		if idleRetentionStr != "" {
			fmt.Sscanf(idleRetentionStr, "%d", &idleRetentionSecs)
		}

		isPersistent := persistentStr == "true"
		instances = append(instances, &sandbox.Instance{
			ID:          id,
			ContainerID: c.ID,
			Status:      sandbox.StatusRunning,
			Config: sandbox.InstanceConfig{
				SessionID: sessionID,
				TenantID:  tenantID,
				Image:     c.Image,
				Name:      name,
				AgentID:   agentID,
			},
			CreatedAt:         time.Unix(c.Created, 0),
			ExpiresAt:         expiresAt,
			LastUsedAt:        lastUsedAt,
			IdleRetentionSecs: idleRetentionSecs,
			Backend:           "docker",
			Name:              name,
			Persistent:        isPersistent,
			AgentID:           agentID,
		})
	}

	return instances, nil
}

// truncateOutput caps output at 1MB to prevent memory exhaustion.
func truncateOutput(s string) string {
	const maxLen = 1 << 20 // 1MB
	if len(s) > maxLen {
		return s[:maxLen] + "\n... (output truncated at 1MB)"
	}
	return s
}

// Ensure DockerBackend implements PortExposer and PortDetector at compile time.
var _ sandbox.PortExposer = (*DockerBackend)(nil)
var _ sandbox.PortDetector = (*DockerBackend)(nil)

// portProxy holds state for a userspace TCP proxy for an exposed port.
type portProxy struct {
	listener      net.Listener
	cancel        context.CancelFunc
	done          chan struct{}
	dialFailures  atomic.Int64 // consecutive upstream dial failures
	relayPID      int          // PID of in-container relay process (0 = no relay)
	containerID   string       // container ID for relay cleanup
	upstreamMode  string       // "direct" or "exec"
	tunnelRuntime string       // runtime used for exec tunnel ("node"/"python3")
}

// ExposePort makes a container port reachable from the host via a userspace TCP proxy.
// Returns the host port that was bound.
func (b *DockerBackend) ExposePort(ctx context.Context, id string, port int, protocol string) (int, error) {
	// Inspect container to get its bridge IP
	inspect, err := b.client.ContainerInspect(ctx, id)
	if err != nil {
		if isContainerGone(err) {
			return 0, fmt.Errorf("failed to inspect container: %w", sandbox.ErrSandboxNotRunning)
		}
		return 0, fmt.Errorf("failed to inspect container: %w", err)
	}

	const (
		upstreamModeDirect = "direct"
		upstreamModeExec   = "exec"
	)

	containerIPs := containerIPCandidates(&inspect)
	if len(containerIPs) == 0 {
		return 0, fmt.Errorf("container has no network IP (network mode may be 'none')")
	}

	upstreamMode := upstreamModeDirect
	tunnelRuntime := ""
	containerIP, routeErr := pickRoutableContainerIP(port, containerIPs)
	if routeErr != nil {
		runtime, runtimeErr := b.detectExecTunnelRuntime(ctx, id)
		if runtimeErr != nil {
			return 0, fmt.Errorf("sandbox container is not routable from proxy process (%v); exec tunnel fallback unavailable: %w", routeErr, runtimeErr)
		}
		upstreamMode = upstreamModeExec
		tunnelRuntime = runtime
		logger.WithFields("sandbox_id", id, "port", port, "route_error", routeErr.Error(), "runtime", runtime).
			Warn("docker_sandbox: no routable container IP; using docker exec tunnel fallback")
	}

	// Listen on a random host port
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, fmt.Errorf("failed to create listener: %w", err)
	}

	hostPort := listener.Addr().(*net.TCPAddr).Port
	target := ""
	if upstreamMode == upstreamModeDirect {
		target = net.JoinHostPort(containerIP, fmt.Sprintf("%d", port))
	}

	proxyCtx, cancel := context.WithCancel(context.Background())
	pp := &portProxy{
		listener:      listener,
		cancel:        cancel,
		done:          make(chan struct{}),
		containerID:   id,
		upstreamMode:  upstreamMode,
		tunnelRuntime: tunnelRuntime,
	}

	// Close any existing proxy for this container+port before storing the new one.
	// This prevents goroutine leaks when re-exposing a port (e.g., after server restart).
	b.proxyMu.Lock()
	if b.portProxies == nil {
		b.portProxies = make(map[string]map[int]*portProxy)
	}
	if b.portProxies[id] == nil {
		b.portProxies[id] = make(map[int]*portProxy)
	}
	if old, exists := b.portProxies[id][port]; exists {
		old.cancel()
		old.listener.Close()
		go func() { <-old.done }() // drain in background
	}
	b.portProxies[id][port] = pp
	b.proxyMu.Unlock()

	// Start proxy goroutine
	go func() {
		defer close(pp.done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-proxyCtx.Done():
					return
				default:
					logger.WithFields("sandbox_id", id, "port", port, "error", err.Error()).
						Debug("docker_sandbox: port proxy accept error")
					return
				}
			}
			if pp.upstreamMode == upstreamModeExec {
				go b.proxyConnectionViaExec(proxyCtx, conn, id, port, pp.tunnelRuntime, &pp.dialFailures)
			} else {
				go proxyConnection(proxyCtx, conn, target, &pp.dialFailures)
			}
		}
	}()

	if pp.upstreamMode == upstreamModeDirect {
		// Quick connectivity check — verify the container port is reachable.
		// This catches common issues (server not started, wrong port) at expose time
		// rather than when the user first accesses the URL.
		checkConn, checkErr := net.DialTimeout("tcp", target, 3*time.Second)
		if checkErr != nil {
			// Port not reachable on bridge IP — likely the process is bound to
			// 127.0.0.1 only (e.g., Vite, Next.js dev server). Start a relay.
			logger.WithFields("sandbox_id", id, "port", port, "target", target).
				Info("docker_sandbox: target not reachable on bridge IP, attempting localhost relay")

			relayPID, relayErr := b.startRelay(ctx, id, containerIP, port)
			if relayErr != nil {
				logger.WithFields("sandbox_id", id, "port", port, "error", relayErr.Error()).
					Warn("docker_sandbox: port exposed but localhost relay failed (server may still be starting or bind to 0.0.0.0)")
			} else {
				pp.relayPID = relayPID
			}

			// Final verification. If still unreachable, fail fast instead of exposing
			// a broken mapping that only produces proxy dial errors.
			finalConn, finalErr := net.DialTimeout("tcp", target, 2*time.Second)
			if finalErr != nil {
				if isRouteError(finalErr) {
					pp.cancel()
					pp.listener.Close()
					<-pp.done

					b.proxyMu.Lock()
					if portMap, ok := b.portProxies[id]; ok {
						delete(portMap, port)
						if len(portMap) == 0 {
							delete(b.portProxies, id)
						}
					}
					b.proxyMu.Unlock()

					return 0, fmt.Errorf("port %d is unreachable at %s after relay attempt: %w", port, target, finalErr)
				}

				logger.WithFields("sandbox_id", id, "port", port, "target", target, "error", finalErr.Error()).
					Warn("docker_sandbox: relay verification failed but keeping proxy active")
			} else {
				finalConn.Close()
			}
		} else {
			checkConn.Close()
		}
	} else {
		logger.WithFields("sandbox_id", id, "port", port, "runtime", pp.tunnelRuntime).
			Info("docker_sandbox: exposing port via docker exec tunnel")
	}

	logger.WithFields(
		"sandbox_id", id,
		"port", port,
		"host_port", hostPort,
		"target", target,
		"relay", pp.relayPID > 0,
		"upstream_mode", pp.upstreamMode,
		"runtime", pp.tunnelRuntime,
	).
		Info("docker_sandbox: port exposed via userspace proxy")

	return hostPort, nil
}

// UnexposePort closes an exposed port proxy and kills any in-container relay.
func (b *DockerBackend) UnexposePort(ctx context.Context, id string, port int) error {
	b.proxyMu.Lock()
	defer b.proxyMu.Unlock()

	if portMap, ok := b.portProxies[id]; ok {
		if pp, ok := portMap[port]; ok {
			// Kill in-container relay process if one was started
			if pp.relayPID > 0 {
				b.killRelay(ctx, pp.containerID, pp.relayPID)
			}
			pp.cancel()
			pp.listener.Close()
			<-pp.done
			delete(portMap, port)
			if len(portMap) == 0 {
				delete(b.portProxies, id)
			}
			logger.WithFields("sandbox_id", id, "port", port).
				Debug("docker_sandbox: port unexposed")
			return nil
		}
	}
	return fmt.Errorf("no proxy found for sandbox %s port %d", id, port)
}

// closeAllProxies closes all port proxies for a sandbox. Called during Destroy.
// Relay processes are killed best-effort; the container is about to be destroyed anyway.
func (b *DockerBackend) closeAllProxies(id string) {
	b.proxyMu.Lock()
	defer b.proxyMu.Unlock()

	if portMap, ok := b.portProxies[id]; ok {
		for port, pp := range portMap {
			if pp.relayPID > 0 {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				b.killRelay(ctx, pp.containerID, pp.relayPID)
				cancel()
			}
			pp.cancel()
			pp.listener.Close()
			<-pp.done
			logger.WithFields("sandbox_id", id, "port", port).
				Debug("docker_sandbox: proxy closed during destroy")
		}
		delete(b.portProxies, id)
	}
}

// proxyConnection copies data bidirectionally between client and target.
// dialFailures tracks consecutive failures to suppress log spam when the
// upstream becomes permanently unreachable (e.g., container destroyed).
func proxyConnection(ctx context.Context, client net.Conn, target string, dialFailures *atomic.Int64) {
	defer client.Close()

	dialer := net.Dialer{Timeout: 5 * time.Second}
	upstream, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		n := dialFailures.Add(1)
		// Log the first failure and then only every 50th to avoid spam
		// when a browser keeps retrying against a dead container.
		if n == 1 || n%50 == 0 {
			logger.WithFields("target", target, "error", err.Error(), "consecutive_failures", n).
				Warn("docker_sandbox: port proxy failed to dial upstream")
		}
		return
	}
	dialFailures.Store(0) // reset on success
	defer upstream.Close()

	done := make(chan struct{})
	go func() {
		io.Copy(upstream, client)
		// Half-close the upstream write side so the server knows the request is done.
		if tc, ok := upstream.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		close(done)
	}()
	io.Copy(client, upstream)
	<-done
}

// proxyConnectionViaExec proxies a TCP client connection into the sandbox via
// Docker Exec stdio when the container IP is not directly routable.
func (b *DockerBackend) proxyConnectionViaExec(ctx context.Context, clientConn net.Conn, containerID string, port int, runtime string, dialFailures *atomic.Int64) {
	defer clientConn.Close()

	cmd, err := execTunnelCommand(runtime, port)
	if err != nil {
		n := dialFailures.Add(1)
		if n == 1 || n%50 == 0 {
			logger.WithFields("sandbox_id", containerID, "port", port, "runtime", runtime, "error", err.Error(), "consecutive_failures", n).
				Warn("docker_sandbox: invalid exec tunnel configuration")
		}
		return
	}

	execResp, err := b.client.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd:          cmd,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		n := dialFailures.Add(1)
		if n == 1 || n%50 == 0 {
			logger.WithFields("sandbox_id", containerID, "port", port, "runtime", runtime, "error", err.Error(), "consecutive_failures", n).
				Warn("docker_sandbox: exec tunnel create failed")
		}
		return
	}

	attachResp, err := b.client.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		n := dialFailures.Add(1)
		if n == 1 || n%50 == 0 {
			logger.WithFields("sandbox_id", containerID, "port", port, "runtime", runtime, "error", err.Error(), "consecutive_failures", n).
				Warn("docker_sandbox: exec tunnel attach failed")
		}
		return
	}
	defer attachResp.Close()
	dialFailures.Store(0)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(attachResp.Conn, clientConn)
		if cw, ok := attachResp.Conn.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()

	_, _ = stdcopy.StdCopy(clientConn, io.Discard, attachResp.Reader)
	<-done
}

func (b *DockerBackend) detectExecTunnelRuntime(ctx context.Context, containerID string) (string, error) {
	detectCtx, detectCancel := context.WithTimeout(ctx, 2*time.Second)
	defer detectCancel()
	whichNode, err := b.Exec(detectCtx, containerID, sandbox.ExecRequest{
		Command: []string{"which", "node"},
		Timeout: 2 * time.Second,
	})
	if err == nil && whichNode.ExitCode == 0 {
		return "node", nil
	}

	detectCtx2, detectCancel2 := context.WithTimeout(ctx, 2*time.Second)
	defer detectCancel2()
	whichPython, err := b.Exec(detectCtx2, containerID, sandbox.ExecRequest{
		Command: []string{"which", "python3"},
		Timeout: 2 * time.Second,
	})
	if err == nil && whichPython.ExitCode == 0 {
		return "python3", nil
	}

	return "", fmt.Errorf("no runtime available for exec tunnel (expected node or python3)")
}

func execTunnelCommand(runtime string, port int) ([]string, error) {
	switch runtime {
	case "node":
		script := fmt.Sprintf(
			`const net=require('net');const s=net.connect(%d,'127.0.0.1');s.on('connect',()=>{process.stdin.pipe(s);s.pipe(process.stdout)});s.on('error',e=>{process.stderr.write((e&&e.message)||String(e));process.exit(1)});process.stdin.on('end',()=>s.end());`,
			port,
		)
		return []string{"node", "-e", script}, nil
	case "python3":
		script := fmt.Sprintf(`import socket,sys,threading
s=socket.create_connection(('127.0.0.1',%d))
def up():
    try:
        while True:
            d=sys.stdin.buffer.read(4096)
            if not d: break
            s.sendall(d)
    except: pass
    try: s.shutdown(socket.SHUT_WR)
    except: pass
t=threading.Thread(target=up,daemon=True)
t.start()
try:
    while True:
        d=s.recv(4096)
        if not d: break
        sys.stdout.buffer.write(d); sys.stdout.buffer.flush()
except: pass`, port)
		return []string{"python3", "-c", script}, nil
	default:
		return nil, fmt.Errorf("unsupported exec tunnel runtime: %s", runtime)
	}
}

// startRelay launches a TCP relay process inside the container that listens on
// containerIP:port and forwards connections to 127.0.0.1:port. This bridges
// the gap when a process (e.g. Vite) binds only to localhost inside the container
// while the host-side proxy connects via the container's bridge IP.
//
// Returns the PID of the relay process for later cleanup.
func (b *DockerBackend) startRelay(ctx context.Context, containerID, containerIP string, port int) (int, error) {
	portStr := strconv.Itoa(port)

	// Try Node.js first (commonly available in dev images)
	nodeScript := fmt.Sprintf(
		`require('net').createServer(c=>{const u=require('net').connect(%d,'127.0.0.1',()=>{c.pipe(u);u.pipe(c)});u.on('error',()=>c.destroy());c.on('error',()=>u.destroy())}).listen(%d,'%s')`,
		port, port, containerIP,
	)
	nodeCmd := fmt.Sprintf("nohup node -e \"%s\" >/dev/null 2>&1 & echo $!", nodeScript)

	// Try python3 as fallback
	pythonScript := fmt.Sprintf(`import socket,threading,sys
def relay(src,dst):
    try:
        while True:
            d=src.recv(4096)
            if not d: break
            dst.sendall(d)
    except: pass
    finally: src.close();dst.close()
s=socket.socket(socket.AF_INET,socket.SOCK_STREAM)
s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
s.bind(('%s',%d))
s.listen(128)
while True:
    c,_=s.accept()
    u=socket.socket(socket.AF_INET,socket.SOCK_STREAM)
    try: u.connect(('127.0.0.1',%d))
    except: c.close();u.close();continue
    threading.Thread(target=relay,args=(c,u),daemon=True).start()
    threading.Thread(target=relay,args=(u,c),daemon=True).start()
`, containerIP, port, port)

	// Detect available runtime and start relay
	var relayPID int

	// Try node first
	detectCtx, detectCancel := context.WithTimeout(ctx, 2*time.Second)
	defer detectCancel()
	whichResult, err := b.Exec(detectCtx, containerID, sandbox.ExecRequest{
		Command: []string{"which", "node"},
		Timeout: 2 * time.Second,
	})

	if err == nil && whichResult.ExitCode == 0 {
		// Node available — start relay
		relayResult, err := b.Exec(ctx, containerID, sandbox.ExecRequest{
			Command: []string{"sh", "-c", nodeCmd},
			Timeout: 5 * time.Second,
		})
		if err == nil && relayResult.ExitCode == 0 {
			pidStr := strings.TrimSpace(relayResult.Stdout)
			if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
				relayPID = pid
			}
		}
	}

	// Fallback to python3
	if relayPID == 0 {
		detectCtx2, detectCancel2 := context.WithTimeout(ctx, 2*time.Second)
		defer detectCancel2()
		whichResult2, err := b.Exec(detectCtx2, containerID, sandbox.ExecRequest{
			Command: []string{"which", "python3"},
			Timeout: 2 * time.Second,
		})

		if err == nil && whichResult2.ExitCode == 0 {
			// Write relay script to /tmp
			scriptPath := fmt.Sprintf("/tmp/_relay_%s.py", portStr)
			if err := b.WriteFile(ctx, containerID, scriptPath, []byte(pythonScript)); err != nil {
				return 0, fmt.Errorf("failed to write python relay script: %w", err)
			}

			pyCmd := fmt.Sprintf("nohup python3 %s >/dev/null 2>&1 & echo $!", scriptPath)
			relayResult, err := b.Exec(ctx, containerID, sandbox.ExecRequest{
				Command: []string{"sh", "-c", pyCmd},
				Timeout: 5 * time.Second,
			})
			if err == nil && relayResult.ExitCode == 0 {
				pidStr := strings.TrimSpace(relayResult.Stdout)
				if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
					relayPID = pid
				}
			}
		}
	}

	if relayPID == 0 {
		return 0, fmt.Errorf("no runtime (node or python3) available to start localhost relay; " +
			"start your server with --host 0.0.0.0 to bind to all interfaces")
	}

	// Wait briefly for relay to start listening
	time.Sleep(500 * time.Millisecond)

	// Verify relay is reachable
	target := net.JoinHostPort(containerIP, portStr)
	verifyConn, err := net.DialTimeout("tcp", target, 3*time.Second)
	if err != nil {
		// Kill the failed relay process
		b.killRelay(ctx, containerID, relayPID)
		return 0, fmt.Errorf("relay started (PID %d) but port %s still not reachable: %w", relayPID, target, err)
	}
	verifyConn.Close()

	logger.WithFields("sandbox_id", containerID, "port", port, "relay_pid", relayPID, "container_ip", containerIP).
		Info("docker_sandbox: localhost relay started")

	return relayPID, nil
}

// killRelay kills a relay process inside a container. Best-effort, errors are logged.
func (b *DockerBackend) killRelay(ctx context.Context, containerID string, pid int) {
	if pid <= 0 {
		return
	}
	killCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, _ = b.Exec(killCtx, containerID, sandbox.ExecRequest{
		Command: []string{"sh", "-c", fmt.Sprintf("kill %d", pid)},
		Timeout: 3 * time.Second,
	})
}

// containerIPCandidates returns deterministic container IP candidates for proxying.
// NetworkSettings.Networks is a map, so its iteration order is random.
func containerIPCandidates(inspect *container.InspectResponse) []string {
	if inspect == nil || inspect.NetworkSettings == nil {
		return nil
	}

	seen := make(map[string]struct{})
	var ips []string

	if ip := strings.TrimSpace(inspect.NetworkSettings.IPAddress); ip != "" {
		seen[ip] = struct{}{}
		ips = append(ips, ip)
	}

	if len(inspect.NetworkSettings.Networks) == 0 {
		return ips
	}

	networkNames := make([]string, 0, len(inspect.NetworkSettings.Networks))
	for name := range inspect.NetworkSettings.Networks {
		networkNames = append(networkNames, name)
	}
	sort.Strings(networkNames)

	for _, name := range networkNames {
		nw := inspect.NetworkSettings.Networks[name]
		if nw == nil {
			continue
		}
		ip := strings.TrimSpace(nw.IPAddress)
		if ip == "" {
			continue
		}
		if _, ok := seen[ip]; ok {
			continue
		}
		seen[ip] = struct{}{}
		ips = append(ips, ip)
	}

	return ips
}

// pickRoutableContainerIP chooses the first IP that is routable from this process.
// For localhost-bound services, a routable IP usually returns connection refused,
// which is acceptable because relay setup can still fix it.
func pickRoutableContainerIP(port int, ips []string) (string, error) {
	if len(ips) == 0 {
		return "", fmt.Errorf("no IP candidates")
	}

	var firstRouteErr error
	for _, ip := range ips {
		target := net.JoinHostPort(ip, strconv.Itoa(port))
		conn, err := net.DialTimeout("tcp", target, 1200*time.Millisecond)
		if err == nil {
			conn.Close()
			return ip, nil
		}
		if isRouteError(err) {
			if firstRouteErr == nil {
				firstRouteErr = err
			}
			continue
		}
		return ip, nil
	}

	if firstRouteErr != nil {
		return "", firstRouteErr
	}
	return "", fmt.Errorf("all candidate container IPs are unreachable")
}

func isRouteError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "no route to host") ||
		strings.Contains(s, "network is unreachable") ||
		strings.Contains(s, "host is unreachable")
}

// DetectListeningPorts discovers TCP ports that are currently listening inside
// the container. It tries `ss -tlnp` first (structured output), then falls back
// to parsing /proc/net/tcp + /proc/net/tcp6 (always available in Linux).
func (b *DockerBackend) DetectListeningPorts(ctx context.Context, id string) ([]sandbox.ListeningPort, error) {
	// Try ss first (more reliable, includes process info)
	ssCtx, ssCancel := context.WithTimeout(ctx, 5*time.Second)
	defer ssCancel()
	ssResult, err := b.Exec(ssCtx, id, sandbox.ExecRequest{
		Command: []string{"ss", "-tlnp"},
		Timeout: 5 * time.Second,
	})
	if err == nil && ssResult.ExitCode == 0 && ssResult.Stdout != "" {
		return parseSSOutput(ssResult.Stdout), nil
	}

	// Fallback: parse /proc/net/tcp and /proc/net/tcp6
	var ports []sandbox.ListeningPort

	for _, proto := range []struct {
		file     string
		protocol string
	}{
		{"/proc/net/tcp", "tcp"},
		{"/proc/net/tcp6", "tcp6"},
	} {
		content, err := b.ReadFile(ctx, id, proto.file)
		if err != nil {
			continue
		}
		ports = append(ports, parseProcNetTCP(string(content), proto.protocol)...)
	}

	return ports, nil
}

// parseSSOutput parses the output of `ss -tlnp` into ListeningPort entries.
// Example line: "LISTEN 0 128 0.0.0.0:5173 0.0.0.0:* users:(("node",pid=42,fd=19))"
func parseSSOutput(output string) []sandbox.ListeningPort {
	var ports []sandbox.ListeningPort
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "LISTEN") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		// fields[3] is the local address:port (e.g., "0.0.0.0:5173", "127.0.0.1:3000", "[::]:8080")
		localAddr := fields[3]
		addr, portStr := parseSSAddr(localAddr)
		portNum, err := strconv.Atoi(portStr)
		if err != nil || portNum == 0 {
			continue
		}

		protocol := "tcp"
		if strings.HasPrefix(addr, "[") || addr == "::" || addr == "::1" {
			protocol = "tcp6"
		}

		lp := sandbox.ListeningPort{
			Port:     portNum,
			Protocol: protocol,
			Address:  addr,
		}

		// Try to extract process info from the users:(...) field
		for _, f := range fields {
			if strings.HasPrefix(f, "users:") {
				lp.Process, lp.PID = parseSSUsers(f)
				break
			}
		}

		ports = append(ports, lp)
	}

	return ports
}

// parseSSAddr splits an ss local address field like "0.0.0.0:5173" or "[::]:8080".
func parseSSAddr(s string) (addr, port string) {
	// Handle IPv6 format [::]:port
	if idx := strings.LastIndex(s, "]:"); idx >= 0 {
		return s[1:idx], s[idx+2:]
	}
	// Handle IPv4 format addr:port
	if idx := strings.LastIndex(s, ":"); idx >= 0 {
		return s[:idx], s[idx+1:]
	}
	return s, ""
}

// parseSSUsers extracts process name and PID from ss users field.
// Format: users:(("node",pid=42,fd=19))
func parseSSUsers(s string) (process string, pid int) {
	// Extract content between ((" and the first quote
	if idx := strings.Index(s, "((\""); idx >= 0 {
		rest := s[idx+3:]
		if end := strings.Index(rest, "\""); end >= 0 {
			process = rest[:end]
		}
	}
	// Extract pid=N
	if idx := strings.Index(s, "pid="); idx >= 0 {
		rest := s[idx+4:]
		end := strings.IndexAny(rest, ",)")
		if end < 0 {
			end = len(rest)
		}
		pid, _ = strconv.Atoi(rest[:end])
	}
	return
}

// parseProcNetTCP parses /proc/net/tcp or /proc/net/tcp6 content.
// Only returns entries in LISTEN state (0A).
// Format: sl local_address rem_address st ...
// Address is hex-encoded: 0100007F:0BB8 = 127.0.0.1:3000
func parseProcNetTCP(content, protocol string) []sandbox.ListeningPort {
	var ports []sandbox.ListeningPort
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		// Skip header
		if fields[0] == "sl" {
			continue
		}
		// State must be 0A (LISTEN)
		if fields[3] != "0A" {
			continue
		}

		localAddr := fields[1]
		parts := strings.Split(localAddr, ":")
		if len(parts) != 2 {
			continue
		}

		portNum64, err := strconv.ParseInt(parts[1], 16, 32)
		if err != nil || portNum64 == 0 {
			continue
		}

		addr := hexToIP(parts[0], protocol)

		ports = append(ports, sandbox.ListeningPort{
			Port:     int(portNum64),
			Protocol: protocol,
			Address:  addr,
		})
	}

	return ports
}

// hexToIP converts a hex-encoded IP address from /proc/net/tcp to a human-readable string.
// IPv4: "0100007F" → "127.0.0.1" (note: little-endian byte order)
// IPv6: 32-hex-char string → "::" or "::1" etc.
func hexToIP(hex, protocol string) string {
	if protocol == "tcp6" {
		if len(hex) == 32 {
			// Check for all zeros (::)
			allZero := true
			for _, c := range hex {
				if c != '0' {
					allZero = false
					break
				}
			}
			if allZero {
				return "::"
			}
			// Check for ::1 (last byte is 01)
			if hex[:30] == "000000000000000000000000000000" && hex[30:] == "01" {
				return "::1"
			}
		}
		return "::" // simplified
	}

	// IPv4: hex is 8 chars, little-endian
	if len(hex) != 8 {
		return "0.0.0.0"
	}
	var octets [4]uint64
	for i := 0; i < 4; i++ {
		octets[i], _ = strconv.ParseUint(hex[i*2:i*2+2], 16, 8)
	}
	// /proc/net/tcp stores IPv4 in little-endian order on little-endian hosts
	return fmt.Sprintf("%d.%d.%d.%d", octets[3], octets[2], octets[1], octets[0])
}
