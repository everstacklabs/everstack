// Package docker provides a Docker-based isolation backend for self-hosted deployments.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/everstacklabs/everstack/internal/functions/isolation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Backend implements isolation.Backend using Docker containers.
type Backend struct {
	client *client.Client
	config Config

	// Container pool for warm containers
	pool *ContainerPool

	// Execution logger for OTEL integration
	execLogger *isolation.ExecutionLogger

	// Stats
	totalExecutions atomic.Int64
	activeRequests  atomic.Int32
	warmHits        atomic.Int64
	coldStarts      atomic.Int64
	totalDurationMs atomic.Int64
	totalErrors     atomic.Int64

	mu      sync.RWMutex
	running bool
}

// Config contains Docker backend configuration.
type Config struct {
	isolation.Config

	// Docker settings
	Host          string   // Docker host (default: auto-detect)
	AutoDetect    bool     // Auto-detect Docker socket location (default: true)
	FallbackHosts []string // Additional hosts to try if auto-detection fails
	ImagePrefix   string   // Image prefix (default: ghcr.io/everstacklabs/runtime)
	NetworkMode   string   // Docker network mode (default: none)
	AutoPull      bool     // Auto-pull images if missing (default: true)

	// Container settings
	ContainerPrefix string // Container name prefix
	CleanupOnExit   bool   // Remove containers after execution

	// Pool settings for warm containers
	Pool PoolConfig
}

// DefaultConfig returns sensible defaults for Docker backend.
func DefaultConfig() Config {
	return Config{
		Config:          isolation.DefaultConfig(),
		AutoDetect:      true,
		ImagePrefix:     "ghcr.io/everstacklabs/runtime",
		NetworkMode:     "none",
		AutoPull:        true,
		ContainerPrefix: "everstack-fn-",
		CleanupOnExit:   true,
		Pool:            DefaultPoolConfig(),
	}
}

// CommonSocketPaths returns common Docker socket paths to try based on the OS.
// These cover Docker Desktop, OrbStack, Colima, Podman, and standard Linux installations.
func CommonSocketPaths() []string {
	home := os.Getenv("HOME")
	if home == "" {
		home = os.Getenv("USERPROFILE") // Windows fallback
	}

	paths := []string{
		"/var/run/docker.sock", // Standard Linux, Docker Desktop symlink
		"/run/docker.sock",     // Alternative Linux path (some distros use /run directly)
	}

	if runtime.GOOS == "darwin" {
		// macOS-specific paths
		paths = append(paths,
			filepath.Join(home, ".orbstack/run/docker.sock"),                             // OrbStack
			filepath.Join(home, ".colima/default/docker.sock"),                           // Colima (default profile)
			filepath.Join(home, ".colima/docker/docker.sock"),                            // Colima (docker profile)
			filepath.Join(home, ".docker/run/docker.sock"),                               // Docker Desktop alternative
			filepath.Join(home, "Library/Containers/com.docker.docker/Data/docker.sock"), // Docker Desktop
		)
	}

	if runtime.GOOS == "linux" {
		// Check if running inside OrbStack's Linux VM
		if isOrbStackLinuxVM() {
			// Inside OrbStack VM, the socket is at ~/.orbstack/run/docker.sock
			paths = append([]string{
				filepath.Join(home, ".orbstack/run/docker.sock"), // OrbStack socket inside VM
			}, paths...)
		}

		// Linux-specific paths (including WSL)
		paths = append(paths,
			filepath.Join(home, ".docker/run/docker.sock"),                            // Rootless Docker
			"/run/user/1000/docker.sock",                                              // Rootless Docker (common UID)
			"/run/podman/podman.sock",                                                 // Podman
			filepath.Join(home, ".local/share/containers/podman/machine/podman.sock"), // Podman machine
		)
	}

	return paths
}

// isOrbStackLinuxVM checks if we're running inside OrbStack's Linux VM.
func isOrbStackLinuxVM() bool {
	// Check for OrbStack-specific indicators
	if _, err := os.Stat("/mnt/mac"); err == nil {
		return true
	}
	// Check if docker command is OrbStack's wrapper
	if target, err := os.Readlink("/opt/orbstack-guest/bin/macctl"); err == nil && target != "" {
		return true
	}
	if _, err := os.Stat("/opt/orbstack-guest"); err == nil {
		return true
	}
	return false
}

// detectDockerHost attempts to find a working Docker socket.
// It tries common socket paths and returns the first one that responds to a ping.
func detectDockerHost(ctx context.Context, cfg Config) (string, error) {
	var hostsToTry []string

	// 1. If explicit host is set, try it first
	if cfg.Host != "" {
		hostsToTry = append(hostsToTry, cfg.Host)
	}

	// 2. Check DOCKER_HOST environment variable
	if envHost := os.Getenv("DOCKER_HOST"); envHost != "" {
		hostsToTry = append(hostsToTry, envHost)
	}

	// 3. If auto-detect is enabled, add common socket paths
	if cfg.AutoDetect {
		for _, path := range CommonSocketPaths() {
			hostsToTry = append(hostsToTry, "unix://"+path)
		}
	}

	// 4. Add any fallback hosts from config
	hostsToTry = append(hostsToTry, cfg.FallbackHosts...)

	// Deduplicate hosts while preserving order
	seen := make(map[string]bool)
	var uniqueHosts []string
	for _, host := range hostsToTry {
		if !seen[host] {
			seen[host] = true
			uniqueHosts = append(uniqueHosts, host)
		}
	}

	// Try each host
	var lastErr error
	for _, host := range uniqueHosts {
		if err := testDockerConnection(ctx, host); err != nil {
			logger.WithFields(
				"host", host,
				"error", err.Error(),
			).Debug("docker socket not available")
			lastErr = err
			continue
		}

		logger.WithFields("host", host).Info("docker socket detected")
		return host, nil
	}

	// Build helpful error message
	return "", buildDockerConnectionError(lastErr, uniqueHosts)
}

// testDockerConnection tests if a Docker host is reachable.
func testDockerConnection(ctx context.Context, host string) error {
	// Quick socket existence check for unix sockets
	if len(host) > 7 && host[:7] == "unix://" {
		socketPath := host[7:]
		if _, err := os.Stat(socketPath); os.IsNotExist(err) {
			return fmt.Errorf("socket does not exist: %s", socketPath)
		}
		// Also verify it's actually a socket
		conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
		if err != nil {
			return fmt.Errorf("cannot connect to socket: %w", err)
		}
		conn.Close()
	}

	// Create a temporary client to test the connection
	opts := []client.Opt{client.WithHost(host), client.WithAPIVersionNegotiation()}
	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	defer cli.Close()

	// Set a short timeout for the ping
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	_, err = cli.Ping(pingCtx)
	if err != nil {
		return fmt.Errorf("ping failed: %w", err)
	}

	return nil
}

// buildDockerConnectionError builds a helpful error message with suggestions.
func buildDockerConnectionError(lastErr error, triedHosts []string) error {
	var suggestion string

	switch runtime.GOOS {
	case "darwin":
		suggestion = `Docker connection failed. Try one of these:
  1. Start Docker Desktop or OrbStack
  2. If using OrbStack, run: sudo orbctl start
  3. Set DOCKER_HOST environment variable explicitly
  4. Configure docker_host in features.isolated_functions`
	case "linux":
		if isOrbStackLinuxVM() {
			suggestion = `Docker connection failed (OrbStack Linux VM detected).

The Docker socket on macOS cannot be accessed from inside OrbStack's Linux VM.
Try one of these:

  1. Run your application on macOS directly (not inside the Linux VM)
  2. Enable Docker TCP in OrbStack settings and set:
     DOCKER_HOST=tcp://host.internal:2375
  3. Use Docker Compose with the socket volume mount:
     volumes:
       - /var/run/docker.sock:/var/run/docker.sock`
		} else {
			suggestion = `Docker connection failed. Try one of these:
  1. Start Docker daemon: sudo systemctl start docker
  2. Add your user to docker group: sudo usermod -aG docker $USER
  3. For rootless Docker, ensure socket is at ~/.docker/run/docker.sock
  4. Set DOCKER_HOST environment variable explicitly`
		}
	default:
		suggestion = `Docker connection failed. Ensure Docker is running and accessible.`
	}

	return fmt.Errorf("%s\n\nTried %d hosts, last error: %v", suggestion, len(triedHosts), lastErr)
}

// New creates a new Docker backend.
// It auto-detects the Docker socket location if AutoDetect is enabled.
func New(cfg Config) (*Backend, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Auto-detect Docker host if needed
	detectedHost, err := detectDockerHost(ctx, cfg)
	if err != nil {
		return nil, err
	}

	// Update config with detected host
	cfg.Host = detectedHost

	// Create client with detected host
	opts := []client.Opt{
		client.WithHost(cfg.Host),
		client.WithAPIVersionNegotiation(),
	}

	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	return &Backend{
		client:     cli,
		config:     cfg,
		execLogger: isolation.NewExecutionLogger("docker"),
	}, nil
}

// NewWithHost creates a new Docker backend with an explicit host (skips auto-detection).
func NewWithHost(cfg Config, host string) (*Backend, error) {
	cfg.Host = host
	cfg.AutoDetect = false

	opts := []client.Opt{
		client.WithHost(host),
		client.WithAPIVersionNegotiation(),
	}

	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	return &Backend{
		client:     cli,
		config:     cfg,
		execLogger: isolation.NewExecutionLogger("docker"),
	}, nil
}

// Name returns the backend name.
func (b *Backend) Name() string {
	return "docker"
}

// Start initializes the Docker backend.
func (b *Backend) Start(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.running {
		return nil
	}

	// Verify Docker connection
	_, err := b.client.Ping(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to Docker: %w", err)
	}

	// Pre-pull runtime images if configured
	if b.config.AutoPull {
		for _, runtime := range isolation.ValidRuntimes() {
			imageName := b.imageForRuntime(runtime)
			if err := b.ensureImage(ctx, imageName); err != nil {
				logger.WithFields(
					"runtime", runtime,
					"image", imageName,
					"error", err.Error(),
				).Warn("failed to pull runtime image, will retry on first use")
			}
		}
	}

	// Initialize container pool if enabled
	if b.config.Pool.Enabled {
		b.pool = NewContainerPool(b.client, b.config.ImagePrefix, b.config.Pool)
		if err := b.pool.Start(ctx); err != nil {
			logger.WithFields("error", err.Error()).Warn("failed to start container pool, falling back to ephemeral containers")
			b.pool = nil
		} else {
			logger.WithFields(
				"min_per_runtime", b.config.Pool.MinContainersPerRuntime,
				"max_per_runtime", b.config.Pool.MaxContainersPerRuntime,
			).Info("container pool enabled")
		}
	}

	b.running = true
	logger.Info("docker isolation backend started")
	return nil
}

// Stop shuts down the Docker backend.
func (b *Backend) Stop(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.running {
		return nil
	}

	// Stop container pool if enabled
	if b.pool != nil {
		if err := b.pool.Stop(ctx); err != nil {
			logger.WithFields("error", err.Error()).Warn("error stopping container pool")
		}
		b.pool = nil
	}

	b.running = false
	b.client.Close()
	logger.Info("docker isolation backend stopped")
	return nil
}

// Execute runs code in a Docker container.
func (b *Backend) Execute(ctx context.Context, req isolation.ExecutionRequest) (*isolation.ExecutionResult, error) {
	b.activeRequests.Add(1)
	defer b.activeRequests.Add(-1)
	defer b.totalExecutions.Add(1)

	// Try to use pool if enabled
	if b.pool != nil {
		return b.executeWithPool(ctx, req)
	}

	// Fall back to ephemeral container execution
	return b.executeEphemeral(ctx, req)
}

// executeWithPool runs code using the warm container pool.
func (b *Backend) executeWithPool(ctx context.Context, req isolation.ExecutionRequest) (*isolation.ExecutionResult, error) {
	// Get image for runtime (ensure it exists)
	imageName := b.imageForRuntime(req.Runtime)
	if err := b.ensureImage(ctx, imageName); err != nil {
		return &isolation.ExecutionResult{
			Success:   false,
			Error:     fmt.Sprintf("failed to get runtime image: %v", err),
			ErrorType: isolation.ErrorTypeRuntime,
		}, nil
	}

	// Determine resource requirements
	memoryMB := int64(req.MemoryMB)
	if memoryMB == 0 {
		memoryMB = int64(b.config.DefaultMemoryMB)
	}
	networkMode := b.dockerNetworkMode(req.NetworkMode)

	// Acquire a container from the pool
	containerID, warm, err := b.pool.Acquire(ctx, req.Runtime, memoryMB, networkMode)
	if err != nil {
		// Fall back to ephemeral execution if pool fails
		logger.WithFields("error", err.Error()).Warn("pool acquisition failed, falling back to ephemeral")
		return b.executeEphemeral(ctx, req)
	}

	// Track warm vs cold starts
	if warm {
		b.warmHits.Add(1)
	} else {
		b.coldStarts.Add(1)
	}

	logger.WithFields(
		"container_id", containerID[:12],
		"warm", warm,
		"runtime", req.Runtime,
	).Debug("executing with pooled container")

	// Log execution start
	b.execLogger.LogExecutionStarted(req, warm)

	// Execute using the pool
	result, err := b.pool.Execute(ctx, containerID, req)

	// Track metrics
	if result != nil {
		b.totalDurationMs.Add(result.DurationMS)
		if !result.Success {
			b.totalErrors.Add(1)
		}
	}

	// Log execution result
	if err != nil || (result != nil && !result.Success) {
		b.execLogger.LogExecutionError(req, result, warm, err)
	} else {
		b.execLogger.LogExecutionCompleted(req, result, warm)
	}

	// Release container back to pool (mark unhealthy if execution failed badly)
	healthy := err == nil && (result == nil || result.ErrorType != isolation.ErrorTypeOOM)
	b.pool.Release(ctx, containerID, healthy)

	return result, err
}

// executeEphemeral runs code in a new ephemeral container (original behavior).
func (b *Backend) executeEphemeral(ctx context.Context, req isolation.ExecutionRequest) (*isolation.ExecutionResult, error) {
	startTime := time.Now()
	b.coldStarts.Add(1)

	// Log execution start (always cold for ephemeral)
	b.execLogger.LogExecutionStarted(req, false)

	// Get image for runtime
	imageName := b.imageForRuntime(req.Runtime)

	// Ensure image exists
	if err := b.ensureImage(ctx, imageName); err != nil {
		return &isolation.ExecutionResult{
			Success:   false,
			Error:     fmt.Sprintf("failed to get runtime image: %v", err),
			ErrorType: isolation.ErrorTypeRuntime,
		}, nil
	}

	// Create execution payload
	payload, err := json.Marshal(map[string]interface{}{
		"request_id":  req.RequestID,
		"function_id": req.FunctionID,
		"code":        req.Code,
		"packages":    req.Packages,
		"arguments":   req.Arguments,
		"timeout_ms":  req.TimeoutMS,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Configure container
	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if timeout == 0 {
		timeout = time.Duration(b.config.DefaultTimeoutMS) * time.Millisecond
	}

	memoryBytes := int64(req.MemoryMB) * 1024 * 1024
	if memoryBytes == 0 {
		memoryBytes = int64(b.config.DefaultMemoryMB) * 1024 * 1024
	}

	cpuCount := int64(req.VCPUs)
	if cpuCount == 0 {
		cpuCount = int64(b.config.DefaultVCPUs)
	}

	containerConfig := &container.Config{
		Image:        imageName,
		Cmd:          []string{string(payload)},
		AttachStdout: true,
		AttachStderr: true,
		Env:          b.buildEnv(req),
	}

	hostConfig := &container.HostConfig{
		AutoRemove: b.config.CleanupOnExit,
		Resources: container.Resources{
			Memory:   memoryBytes,
			NanoCPUs: cpuCount * 1e9,
		},
		NetworkMode: container.NetworkMode(b.dockerNetworkMode(req.NetworkMode)),
	}

	networkConfig := &network.NetworkingConfig{}

	// Create container
	containerName := fmt.Sprintf("%s%s", b.config.ContainerPrefix, req.RequestID)
	resp, err := b.client.ContainerCreate(ctx, containerConfig, hostConfig, networkConfig, nil, containerName)
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	containerID := resp.ID

	// Ensure cleanup
	defer func() {
		if b.config.CleanupOnExit {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			b.client.ContainerRemove(cleanupCtx, containerID, container.RemoveOptions{Force: true})
		}
	}()

	// Start container
	if err := b.client.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	// Wait for container with timeout
	waitCtx, cancel := context.WithTimeout(ctx, timeout+5*time.Second)
	defer cancel()

	statusCh, errCh := b.client.ContainerWait(waitCtx, containerID, container.WaitConditionNotRunning)

	var exitCode int64
	select {
	case err := <-errCh:
		if err != nil {
			// Check if it's a timeout
			if waitCtx.Err() == context.DeadlineExceeded {
				b.client.ContainerKill(context.Background(), containerID, "KILL")
				return &isolation.ExecutionResult{
					Success:    false,
					Error:      "execution timeout exceeded",
					ErrorType:  isolation.ErrorTypeTimeout,
					DurationMS: time.Since(startTime).Milliseconds(),
				}, nil
			}
			return nil, fmt.Errorf("error waiting for container: %w", err)
		}
	case status := <-statusCh:
		exitCode = status.StatusCode
	}

	// Get logs
	stdout, stderr, err := b.getContainerLogs(ctx, containerID)
	if err != nil {
		logger.WithFields("error", err.Error()).Warn("failed to get container logs")
	}

	durationMS := time.Since(startTime).Milliseconds()

	// Parse result from stdout
	result := b.parseResult(stdout, stderr, exitCode)
	result.DurationMS = durationMS
	result.Stdout = stdout
	result.Stderr = stderr

	// Track metrics
	b.totalDurationMs.Add(durationMS)
	if !result.Success {
		b.totalErrors.Add(1)
	}

	// Log execution result
	if !result.Success {
		b.execLogger.LogExecutionError(req, result, false, nil)
	} else {
		b.execLogger.LogExecutionCompleted(req, result, false)
	}

	return result, nil
}

// SupportsRuntime checks if a runtime is supported.
func (b *Backend) SupportsRuntime(runtime isolation.Runtime) bool {
	switch runtime {
	case isolation.RuntimeNodeJS20, isolation.RuntimeDeno, isolation.RuntimePython3:
		return true
	default:
		return false
	}
}

// Stats returns backend statistics.
func (b *Backend) Stats() isolation.BackendStats {
	stats := isolation.BackendStats{
		Name:            "docker",
		ActiveRequests:  int(b.activeRequests.Load()),
		TotalExecutions: b.totalExecutions.Load(),
		WarmHits:        b.warmHits.Load(),
		ColdStarts:      b.coldStarts.Load(),
		TotalDurationMs: b.totalDurationMs.Load(),
		TotalErrors:     b.totalErrors.Load(),
		RuntimeStats: map[isolation.Runtime]isolation.RuntimeStats{
			isolation.RuntimeNodeJS20: {},
			isolation.RuntimeDeno:     {},
			isolation.RuntimePython3:  {},
		},
	}

	// Include pool statistics if available
	if b.pool != nil {
		poolStats := b.pool.Stats()
		for runtime, runtimeStats := range poolStats.RuntimeStats {
			stats.RuntimeStats[runtime] = isolation.RuntimeStats{
				Ready:     runtimeStats.Warm,
				Executing: runtimeStats.InUse,
				Total:     runtimeStats.Total,
			}
		}

		// Include pool metrics
		stats.PoolMetrics = &isolation.PoolMetrics{
			TotalRecycled:         poolStats.TotalRecycled,
			TotalEvictedIdle:      poolStats.TotalEvictedIdle,
			TotalEvictedUnhealthy: poolStats.TotalEvictedUnhealthy,
		}
	}

	return stats
}

// PoolStats returns detailed container pool statistics.
// Returns nil if pooling is not enabled.
func (b *Backend) PoolStats() *PoolStats {
	if b.pool == nil {
		return nil
	}
	stats := b.pool.Stats()
	return &stats
}

// WarmHits returns the number of warm container hits.
func (b *Backend) WarmHits() int64 {
	return b.warmHits.Load()
}

// ColdStarts returns the number of cold starts.
func (b *Backend) ColdStarts() int64 {
	return b.coldStarts.Load()
}

// imageForRuntime returns the Docker image for a runtime.
func (b *Backend) imageForRuntime(runtime isolation.Runtime) string {
	return fmt.Sprintf("%s:%s", b.config.ImagePrefix, runtime)
}

// ensureImage ensures the image exists, pulling if necessary.
func (b *Backend) ensureImage(ctx context.Context, imageName string) error {
	// Check if image exists locally
	_, err := b.client.ImageInspect(ctx, imageName)
	if err == nil {
		return nil // Image exists
	}

	if !b.config.AutoPull {
		return fmt.Errorf("image %s not found and auto-pull disabled", imageName)
	}

	logger.WithFields("image", imageName).Info("pulling runtime image")

	reader, err := b.client.ImagePull(ctx, imageName, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}
	defer reader.Close()

	// Wait for pull to complete
	_, err = io.Copy(io.Discard, reader)
	return err
}

// buildEnv builds environment variables for the container.
func (b *Backend) buildEnv(req isolation.ExecutionRequest) []string {
	env := []string{
		fmt.Sprintf("REQUEST_ID=%s", req.RequestID),
		fmt.Sprintf("FUNCTION_ID=%s", req.FunctionID),
		fmt.Sprintf("TIMEOUT_MS=%d", req.TimeoutMS),
	}

	// Add allowed hosts for whitelist mode
	if req.NetworkMode == isolation.NetworkWhitelist && len(req.AllowedHosts) > 0 {
		hostsJSON, _ := json.Marshal(req.AllowedHosts)
		env = append(env, fmt.Sprintf("ALLOWED_HOSTS=%s", hostsJSON))
	}

	return env
}

// dockerNetworkMode converts our network mode to Docker's.
func (b *Backend) dockerNetworkMode(mode isolation.NetworkMode) string {
	switch mode {
	case isolation.NetworkAllow:
		return "bridge"
	case isolation.NetworkWhitelist:
		return "bridge" // Network allowed, but host filtering done in container
	case isolation.NetworkDeny:
		fallthrough
	default:
		return "none"
	}
}

// getContainerLogs retrieves stdout and stderr from a container.
func (b *Backend) getContainerLogs(ctx context.Context, containerID string) (string, string, error) {
	opts := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	}

	reader, err := b.client.ContainerLogs(ctx, containerID, opts)
	if err != nil {
		return "", "", err
	}
	defer reader.Close()

	var stdout, stderr bytes.Buffer

	// Docker multiplexes stdout/stderr with a header
	// For simplicity, read all as stdout (container should output JSON result)
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", "", err
	}

	// Parse multiplexed stream
	// First 8 bytes are header: [type(1), 0, 0, 0, size(4)]
	for len(data) >= 8 {
		streamType := data[0]
		size := int(data[4])<<24 | int(data[5])<<16 | int(data[6])<<8 | int(data[7])

		if len(data) < 8+size {
			break
		}

		content := data[8 : 8+size]
		if streamType == 1 {
			stdout.Write(content)
		} else {
			stderr.Write(content)
		}

		data = data[8+size:]
	}

	return stdout.String(), stderr.String(), nil
}

// parseResult parses the execution result from container output.
func (b *Backend) parseResult(stdout, stderr string, exitCode int64) *isolation.ExecutionResult {
	result := &isolation.ExecutionResult{
		Success: exitCode == 0,
	}

	if exitCode != 0 {
		result.ErrorType = isolation.ErrorTypeRuntime
		result.Error = stderr
		if result.Error == "" {
			result.Error = fmt.Sprintf("container exited with code %d", exitCode)
		}

		// Check for OOM
		if exitCode == 137 {
			result.ErrorType = isolation.ErrorTypeOOM
			result.Error = "out of memory"
		}
		return result
	}

	// Try to parse JSON result from stdout
	// Look for the last line containing __result__
	lines := bytes.Split([]byte(stdout), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}

		var output struct {
			Result interface{} `json:"__result__"`
		}
		if err := json.Unmarshal(line, &output); err == nil && output.Result != nil {
			result.Result = output.Result
			return result
		}

		// Also try direct result format from runner
		var directResult struct {
			Success bool        `json:"success"`
			Result  interface{} `json:"result"`
			Error   string      `json:"error"`
		}
		if err := json.Unmarshal(line, &directResult); err == nil {
			result.Success = directResult.Success
			result.Result = directResult.Result
			result.Error = directResult.Error
			return result
		}
	}

	// No structured output, return stdout as result
	result.Result = stdout
	return result
}
