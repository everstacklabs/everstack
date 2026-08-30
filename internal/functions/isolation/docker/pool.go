// Package docker provides a Docker-based isolation backend for self-hosted deployments.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/everstacklabs/everstack/internal/functions/isolation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// PoolConfig contains configuration for the container pool.
type PoolConfig struct {
	// Enable pooling (default: false for backward compatibility)
	Enabled bool

	// MinContainersPerRuntime is the minimum warm containers to maintain per runtime
	MinContainersPerRuntime int

	// MaxContainersPerRuntime is the maximum containers per runtime
	MaxContainersPerRuntime int

	// IdleTimeout is how long a container can be idle before being removed
	IdleTimeout time.Duration

	// MaxUses is the maximum number of times a container can be reused (0 = unlimited)
	MaxUses int

	// WarmupOnStart creates warm containers when the pool starts
	WarmupOnStart bool
}

// DefaultPoolConfig returns sensible defaults for container pooling.
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		Enabled:                 false,  // Disabled by default
		MinContainersPerRuntime: 1,
		MaxContainersPerRuntime: 10,
		IdleTimeout:             5 * time.Minute,
		MaxUses:                 100,  // Recycle after 100 executions
		WarmupOnStart:           false, // Containers created on-demand, not on startup
	}
}

// ContainerPool manages a pool of warm containers for fast execution.
type ContainerPool struct {
	client      *client.Client
	config      PoolConfig
	imagePrefix string

	mu          sync.Mutex
	pools       map[isolation.Runtime]*runtimePool
	running     bool
	stopCh      chan struct{}
	cleanupDone chan struct{}

	// Eviction metrics
	totalRecycled         int64
	totalEvictedIdle      int64
	totalEvictedUnhealthy int64
}

// runtimePool manages containers for a specific runtime.
type runtimePool struct {
	runtime    isolation.Runtime
	containers []*pooledContainer
	mu         sync.Mutex
	activated  bool // true once this runtime has been used at least once (demand-driven)
}

// pooledContainer represents a warm container in the pool.
type pooledContainer struct {
	id          string
	runtime     isolation.Runtime
	createdAt   time.Time
	lastUsedAt  time.Time
	useCount    int
	inUse       bool
	healthy     bool
	memoryMB    int64
	networkMode string
}

// NewContainerPool creates a new container pool.
func NewContainerPool(cli *client.Client, imagePrefix string, config PoolConfig) *ContainerPool {
	return &ContainerPool{
		client:      cli,
		config:      config,
		imagePrefix: imagePrefix,
		pools:       make(map[isolation.Runtime]*runtimePool),
		stopCh:      make(chan struct{}),
		cleanupDone: make(chan struct{}),
	}
}

// Start initializes the pool and optionally warms up containers.
func (p *ContainerPool) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return nil
	}

	// Initialize pools for each runtime
	for _, runtime := range isolation.ValidRuntimes() {
		p.pools[runtime] = &runtimePool{
			runtime:    runtime,
			containers: make([]*pooledContainer, 0),
		}
	}

	p.running = true

	// Clean up orphan containers from previous runs
	p.cleanupOrphans(ctx)

	// Start cleanup goroutine
	go p.cleanupLoop()

	// Warm up containers if configured
	if p.config.WarmupOnStart {
		go p.warmup(context.Background())
	}

	logger.WithFields(
		"min_per_runtime", p.config.MinContainersPerRuntime,
		"max_per_runtime", p.config.MaxContainersPerRuntime,
		"idle_timeout", p.config.IdleTimeout,
	).Info("container pool started")

	return nil
}

// Stop gracefully shuts down the pool and removes all containers.
func (p *ContainerPool) Stop(ctx context.Context) error {
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return nil
	}
	p.running = false
	close(p.stopCh)
	p.mu.Unlock()

	// Wait for cleanup goroutine to finish
	<-p.cleanupDone

	// Remove all pooled containers
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, pool := range p.pools {
		pool.mu.Lock()
		for _, c := range pool.containers {
			p.removeContainer(ctx, c.id)
		}
		pool.containers = nil
		pool.mu.Unlock()
	}

	logger.Info("container pool stopped")
	return nil
}

// cleanupOrphans removes containers from previous runs that were left behind
// (e.g. after an ungraceful shutdown). Identified by the everstack.pool label.
func (p *ContainerPool) cleanupOrphans(ctx context.Context) {
	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	containers, err := p.client.ContainerList(listCtx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", poolLabelKey+"="+poolLabelValue),
		),
	})
	if err != nil {
		logger.WithFields("error", err.Error()).Warn("failed to list orphan pool containers")
		return
	}

	if len(containers) == 0 {
		return
	}

	logger.WithFields("count", len(containers)).Info("cleaning up orphan pool containers from previous run")
	for _, c := range containers {
		p.removeContainer(ctx, c.ID)
	}
}

// Acquire gets a warm container from the pool or creates a new one.
// Returns the container ID and whether it was from the pool (warm) or newly created (cold).
func (p *ContainerPool) Acquire(ctx context.Context, runtime isolation.Runtime, memoryMB int64, networkMode string) (string, bool, error) {
	p.mu.Lock()
	pool, ok := p.pools[runtime]
	p.mu.Unlock()

	if !ok {
		return "", false, fmt.Errorf("unsupported runtime: %s", runtime)
	}

	// Mark runtime as activated so the cleanup loop will replenish it
	pool.mu.Lock()
	pool.activated = true
	pool.mu.Unlock()

	// Try to get a warm container from the pool
	pool.mu.Lock()
	var staleContainers []string
	for i, c := range pool.containers {
		// Find an available container with matching config
		if !c.inUse && c.healthy && c.memoryMB >= memoryMB && c.networkMode == networkMode {
			// Verify container is actually running before acquiring
			inspect, err := p.client.ContainerInspect(ctx, c.id)
			if err != nil || !inspect.State.Running {
				// Container is not running, mark as stale
				c.healthy = false
				staleContainers = append(staleContainers, c.id)
				logger.WithFields(
					"container_id", c.id[:12],
					"runtime", runtime,
				).Debug("container no longer running, marking as stale")
				continue
			}

			c.inUse = true
			c.lastUsedAt = time.Now()
			c.useCount++
			pool.mu.Unlock()

			logger.WithFields(
				"container_id", c.id[:12],
				"runtime", runtime,
				"use_count", c.useCount,
			).Debug("acquired warm container from pool")

			return c.id, true, nil
		}
		_ = i // unused
	}
	pool.mu.Unlock()

	// Clean up stale containers in background
	if len(staleContainers) > 0 {
		go func() {
			for _, id := range staleContainers {
				p.Release(context.Background(), id, false)
			}
		}()
	}

	// No warm container available, create a new one
	containerID, err := p.createWarmContainer(ctx, runtime, memoryMB, networkMode)
	if err != nil {
		return "", false, err
	}

	// Add to pool as in-use
	pool.mu.Lock()
	pool.containers = append(pool.containers, &pooledContainer{
		id:          containerID,
		runtime:     runtime,
		createdAt:   time.Now(),
		lastUsedAt:  time.Now(),
		useCount:    1,
		inUse:       true,
		healthy:     true,
		memoryMB:    memoryMB,
		networkMode: networkMode,
	})
	pool.mu.Unlock()

	logger.WithFields(
		"container_id", containerID[:12],
		"runtime", runtime,
	).Debug("created new container (pool miss)")

	return containerID, false, nil
}

// Release returns a container to the pool or marks it for removal.
func (p *ContainerPool) Release(ctx context.Context, containerID string, healthy bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, pool := range p.pools {
		pool.mu.Lock()
		for i, c := range pool.containers {
			if c.id == containerID {
				c.inUse = false
				c.healthy = healthy

				// Check if container should be recycled
				shouldRemove := !healthy || (p.config.MaxUses > 0 && c.useCount >= p.config.MaxUses)

				if shouldRemove {
					// Remove from pool and destroy
					pool.containers = append(pool.containers[:i], pool.containers[i+1:]...)
					pool.mu.Unlock()
					p.removeContainer(ctx, containerID)

					reason := "unhealthy"
					if healthy {
						reason = "max uses reached"
						p.totalRecycled++
					} else {
						p.totalEvictedUnhealthy++
					}
					logger.WithFields(
						"container_id", containerID[:12],
						"reason", reason,
						"use_count", c.useCount,
					).Debug("container removed from pool")
					return
				}

				pool.mu.Unlock()
				logger.WithFields(
					"container_id", containerID[:12],
					"use_count", c.useCount,
				).Debug("container returned to pool")
				return
			}
		}
		pool.mu.Unlock()
	}
}

// Execute runs code in a pooled container using docker exec.
func (p *ContainerPool) Execute(ctx context.Context, containerID string, req isolation.ExecutionRequest) (*isolation.ExecutionResult, error) {
	startTime := time.Now()

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

	// Determine the exec command based on runtime
	var cmd []string
	switch req.Runtime {
	case isolation.RuntimeNodeJS20:
		cmd = []string{"node", "/app/runner.js", string(payload)}
	case isolation.RuntimeDeno:
		cmd = []string{"deno", "run", "--allow-all", "/app/runner.ts", string(payload)}
	case isolation.RuntimePython3:
		cmd = []string{"python3", "/app/runner.py", string(payload)}
	default:
		return nil, fmt.Errorf("unsupported runtime: %s", req.Runtime)
	}

	// Create exec configuration
	execConfig := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
		Env:          p.buildEnv(req),
	}

	// Create exec instance
	execResp, err := p.client.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return &isolation.ExecutionResult{
			Success:    false,
			Error:      fmt.Sprintf("failed to create exec: %v", err),
			ErrorType:  isolation.ErrorTypeRuntime,
			DurationMS: time.Since(startTime).Milliseconds(),
		}, nil
	}

	// Attach to exec to get output
	attachResp, err := p.client.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return &isolation.ExecutionResult{
			Success:    false,
			Error:      fmt.Sprintf("failed to attach exec: %v", err),
			ErrorType:  isolation.ErrorTypeRuntime,
			DurationMS: time.Since(startTime).Milliseconds(),
		}, nil
	}
	defer attachResp.Close()

	// Set up timeout
	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Read output with timeout
	outputCh := make(chan []byte, 1)
	errCh := make(chan error, 1)

	go func() {
		data, err := io.ReadAll(attachResp.Reader)
		if err != nil {
			errCh <- err
			return
		}
		outputCh <- data
	}()

	var output []byte
	select {
	case <-execCtx.Done():
		return &isolation.ExecutionResult{
			Success:    false,
			Error:      "execution timeout exceeded",
			ErrorType:  isolation.ErrorTypeTimeout,
			DurationMS: time.Since(startTime).Milliseconds(),
		}, nil
	case err := <-errCh:
		return &isolation.ExecutionResult{
			Success:    false,
			Error:      fmt.Sprintf("failed to read output: %v", err),
			ErrorType:  isolation.ErrorTypeRuntime,
			DurationMS: time.Since(startTime).Milliseconds(),
		}, nil
	case output = <-outputCh:
		// Success, continue processing
	}

	// Check exec exit code
	inspectResp, err := p.client.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return &isolation.ExecutionResult{
			Success:    false,
			Error:      fmt.Sprintf("failed to inspect exec: %v", err),
			ErrorType:  isolation.ErrorTypeRuntime,
			DurationMS: time.Since(startTime).Milliseconds(),
		}, nil
	}

	// Parse output
	stdout, stderr := p.parseMultiplexedOutput(output)
	durationMS := time.Since(startTime).Milliseconds()

	result := p.parseResult(stdout, stderr, int64(inspectResp.ExitCode))
	result.DurationMS = durationMS
	result.Stdout = stdout
	result.Stderr = stderr

	return result, nil
}

// Stats returns pool statistics.
func (p *ContainerPool) Stats() PoolStats {
	p.mu.Lock()
	defer p.mu.Unlock()

	stats := PoolStats{
		RuntimeStats:          make(map[isolation.Runtime]RuntimePoolStats),
		TotalRecycled:         p.totalRecycled,
		TotalEvictedIdle:      p.totalEvictedIdle,
		TotalEvictedUnhealthy: p.totalEvictedUnhealthy,
	}

	for runtime, pool := range p.pools {
		pool.mu.Lock()
		var warm, inUse, unhealthy int
		for _, c := range pool.containers {
			if c.inUse {
				inUse++
			} else if c.healthy {
				warm++
			} else {
				unhealthy++
			}
		}
		stats.RuntimeStats[runtime] = RuntimePoolStats{
			Warm:      warm,
			InUse:     inUse,
			Unhealthy: unhealthy,
			Total:     len(pool.containers),
		}
		stats.TotalContainers += len(pool.containers)
		stats.WarmContainers += warm
		pool.mu.Unlock()
	}

	return stats
}

// PoolStats contains pool statistics.
type PoolStats struct {
	TotalContainers int
	WarmContainers  int
	RuntimeStats    map[isolation.Runtime]RuntimePoolStats

	// Eviction metrics
	TotalRecycled         int64
	TotalEvictedIdle      int64
	TotalEvictedUnhealthy int64
}

// RuntimePoolStats contains statistics for a specific runtime pool.
type RuntimePoolStats struct {
	Warm      int
	InUse     int
	Unhealthy int
	Total     int
}

// poolContainerLabels are labels applied to all pool-managed containers for identification.
const poolLabelKey = "everstack.pool"
const poolLabelValue = "warm"
const poolRuntimeLabelKey = "everstack.pool.runtime"

// createWarmContainer creates a new container that stays running and waits for exec.
func (p *ContainerPool) createWarmContainer(ctx context.Context, runtime isolation.Runtime, memoryMB int64, networkMode string) (string, error) {
	imageName := fmt.Sprintf("%s:%s", p.imagePrefix, runtime)

	// Container config - use tail to keep container running
	// IMPORTANT: Override entrypoint to empty string since the runtime image has
	// ENTRYPOINT ["node", "/app/runner.js"]. Without this, CMD is appended to entrypoint.
	containerConfig := &container.Config{
		Image:      imageName,
		Entrypoint: []string{}, // Override image entrypoint
		Cmd:        []string{"tail", "-f", "/dev/null"}, // Keep container running
		Env: []string{
			"POOL_MODE=true",
		},
		Labels: map[string]string{
			poolLabelKey:        poolLabelValue,
			poolRuntimeLabelKey: string(runtime),
		},
	}

	if memoryMB == 0 {
		memoryMB = 512
	}

	hostConfig := &container.HostConfig{
		Resources: container.Resources{
			Memory:   memoryMB * 1024 * 1024,
			NanoCPUs: 1e9, // 1 CPU
		},
		NetworkMode: container.NetworkMode(networkMode),
	}

	// Create container
	resp, err := p.client.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("failed to create warm container: %w", err)
	}

	// Start container
	if err := p.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		// Clean up on failure
		p.removeContainer(ctx, resp.ID)
		return "", fmt.Errorf("failed to start warm container: %w", err)
	}

	return resp.ID, nil
}

// removeContainer forcefully removes a container.
func (p *ContainerPool) removeContainer(ctx context.Context, containerID string) {
	removeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	err := p.client.ContainerRemove(removeCtx, containerID, container.RemoveOptions{
		Force: true,
	})
	if err != nil {
		logger.WithFields(
			"container_id", containerID[:12],
			"error", err.Error(),
		).Warn("failed to remove container")
	}
}

// warmup pre-creates minimum number of warm containers.
func (p *ContainerPool) warmup(ctx context.Context) {
	logger.Info("warming up container pool")

	for _, runtime := range isolation.ValidRuntimes() {
		for i := 0; i < p.config.MinContainersPerRuntime; i++ {
			containerID, err := p.createWarmContainer(ctx, runtime, 512, "none")
			if err != nil {
				logger.WithFields(
					"runtime", runtime,
					"error", err.Error(),
				).Warn("failed to create warm container during warmup")
				continue
			}

			// Verify container is actually running before adding to pool
			inspect, err := p.client.ContainerInspect(ctx, containerID)
			if err != nil || !inspect.State.Running {
				logger.WithFields(
					"container_id", containerID[:12],
					"runtime", runtime,
				).Warn("warm container not running after creation, cleaning up")
				p.removeContainer(ctx, containerID)
				continue
			}

			p.mu.Lock()
			pool := p.pools[runtime]
			p.mu.Unlock()

			pool.mu.Lock()
			pool.activated = true // warmup implies activation for replenish
			pool.containers = append(pool.containers, &pooledContainer{
				id:          containerID,
				runtime:     runtime,
				createdAt:   time.Now(),
				lastUsedAt:  time.Now(),
				useCount:    0,
				inUse:       false,
				healthy:     true,
				memoryMB:    512,
				networkMode: "none",
			})
			pool.mu.Unlock()

			logger.WithFields(
				"container_id", containerID[:12],
				"runtime", runtime,
			).Debug("created warm container during warmup")
		}
	}

	stats := p.Stats()
	logger.WithFields(
		"total_containers", stats.TotalContainers,
		"warm_containers", stats.WarmContainers,
	).Info("container pool warmup complete")
}

// cleanupLoop periodically removes idle and unhealthy containers.
func (p *ContainerPool) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	defer close(p.cleanupDone)

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.cleanup()
		}
	}
}

// cleanup removes idle and unhealthy containers while maintaining minimums.
func (p *ContainerPool) cleanup() {
	ctx := context.Background()
	now := time.Now()

	p.mu.Lock()
	pools := make(map[isolation.Runtime]*runtimePool, len(p.pools))
	for k, v := range p.pools {
		pools[k] = v
	}
	p.mu.Unlock()

	for _, pool := range pools {
		pool.mu.Lock()

		// Count healthy, available containers
		var healthyCount int
		for _, c := range pool.containers {
			if !c.inUse && c.healthy {
				healthyCount++
			}
		}

		// Find containers to remove
		var toRemove []string
		var removeReasons []string
		var newContainers []*pooledContainer

		for _, c := range pool.containers {
			shouldRemove := false
			reason := ""

			// Remove unhealthy containers
			if !c.healthy {
				shouldRemove = true
				reason = "unhealthy"
			}

			// Remove idle containers beyond minimum (if not in use)
			if !c.inUse && healthyCount > p.config.MinContainersPerRuntime {
				idleTime := now.Sub(c.lastUsedAt)
				if idleTime > p.config.IdleTimeout {
					shouldRemove = true
					reason = "idle"
					healthyCount--
				}
			}

			if shouldRemove && !c.inUse {
				toRemove = append(toRemove, c.id)
				removeReasons = append(removeReasons, reason)
			} else {
				newContainers = append(newContainers, c)
			}
		}

		pool.containers = newContainers
		pool.mu.Unlock()

		// Remove containers outside the lock and track metrics
		p.mu.Lock()
		for i, id := range toRemove {
			p.removeContainer(ctx, id)
			if removeReasons[i] == "idle" {
				p.totalEvictedIdle++
			} else {
				p.totalEvictedUnhealthy++
			}
			logger.WithFields(
				"container_id", id[:12],
				"runtime", pool.runtime,
				"reason", removeReasons[i],
			).Debug("removed container during cleanup")
		}
		p.mu.Unlock()

		// Replenish if below minimum — only for runtimes that have been used (demand-driven).
		// This avoids eagerly creating containers for all runtimes on startup.
		pool.mu.Lock()
		activated := pool.activated
		currentCount := 0
		for _, c := range pool.containers {
			if c.healthy && !c.inUse {
				currentCount++
			}
		}
		needed := 0
		if activated {
			needed = p.config.MinContainersPerRuntime - currentCount
		}
		pool.mu.Unlock()

		for i := 0; i < needed; i++ {
			containerID, err := p.createWarmContainer(ctx, pool.runtime, 512, "none")
			if err != nil {
				logger.WithFields(
					"runtime", pool.runtime,
					"error", err.Error(),
				).Warn("failed to replenish warm container")
				continue
			}

			pool.mu.Lock()
			pool.containers = append(pool.containers, &pooledContainer{
				id:          containerID,
				runtime:     pool.runtime,
				createdAt:   time.Now(),
				lastUsedAt:  time.Now(),
				useCount:    0,
				inUse:       false,
				healthy:     true,
				memoryMB:    512,
				networkMode: "none",
			})
			pool.mu.Unlock()
		}
	}
}

// buildEnv builds environment variables for execution.
func (p *ContainerPool) buildEnv(req isolation.ExecutionRequest) []string {
	env := []string{
		fmt.Sprintf("REQUEST_ID=%s", req.RequestID),
		fmt.Sprintf("FUNCTION_ID=%s", req.FunctionID),
		fmt.Sprintf("TIMEOUT_MS=%d", req.TimeoutMS),
	}

	if req.NetworkMode == isolation.NetworkWhitelist && len(req.AllowedHosts) > 0 {
		hostsJSON, _ := json.Marshal(req.AllowedHosts)
		env = append(env, fmt.Sprintf("ALLOWED_HOSTS=%s", hostsJSON))
	}

	return env
}

// parseMultiplexedOutput parses Docker's multiplexed stdout/stderr stream.
func (p *ContainerPool) parseMultiplexedOutput(data []byte) (stdout, stderr string) {
	var stdoutBuf, stderrBuf bytes.Buffer

	for len(data) >= 8 {
		streamType := data[0]
		size := int(data[4])<<24 | int(data[5])<<16 | int(data[6])<<8 | int(data[7])

		if len(data) < 8+size {
			break
		}

		content := data[8 : 8+size]
		if streamType == 1 {
			stdoutBuf.Write(content)
		} else {
			stderrBuf.Write(content)
		}

		data = data[8+size:]
	}

	return stdoutBuf.String(), stderrBuf.String()
}

// parseResult parses the execution result from container output.
func (p *ContainerPool) parseResult(stdout, stderr string, exitCode int64) *isolation.ExecutionResult {
	result := &isolation.ExecutionResult{
		Success: exitCode == 0,
	}

	if exitCode != 0 {
		result.ErrorType = isolation.ErrorTypeRuntime
		result.Error = stderr
		if result.Error == "" {
			result.Error = fmt.Sprintf("execution exited with code %d", exitCode)
		}

		if exitCode == 137 {
			result.ErrorType = isolation.ErrorTypeOOM
			result.Error = "out of memory"
		}
		return result
	}

	// Try to parse JSON result from stdout
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

	result.Result = stdout
	return result
}
