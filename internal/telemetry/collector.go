package telemetry

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"
)

// CollectorManager manages the lifecycle of the embedded OTEL collector sidecar
type CollectorManager struct {
	cmd    *exec.Cmd
	config DirectExportConfig
}

// StartEmbeddedCollector starts the OTEL collector as a sidecar process
// for self-hosted deployments with direct ClickHouse export
func StartEmbeddedCollector(cfg DirectExportConfig) (*CollectorManager, error) {
	if !cfg.Enabled {
		return nil, nil // Collector not enabled
	}

	// Check if collector is already running on localhost:4317
	if isCollectorRunning() {
		return &CollectorManager{config: cfg}, nil
	}

	// Find otelcol-contrib binary
	collectorBinary, err := exec.LookPath("otelcol-contrib")
	if err != nil {
		return nil, fmt.Errorf("otelcol-contrib not found: %w", err)
	}

	// Set environment variables for collector config
	env := os.Environ()
	env = append(env,
		fmt.Sprintf("CLICKHOUSE_HOST=%s", cfg.ClickHouseHost),
		fmt.Sprintf("CLICKHOUSE_DATABASE=%s", cfg.Database),
		fmt.Sprintf("CLICKHOUSE_USERNAME=%s", cfg.Username),
		fmt.Sprintf("CLICKHOUSE_PASSWORD=%s", cfg.Password),
	)

	// Start collector process
	cmd := exec.Command(collectorBinary, "--config", "build/otel-collector-embedded.yaml")
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start collector: %w", err)
	}

	// Wait for collector to be ready (max 10 seconds)
	if err := waitForCollector(10 * time.Second); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("collector failed to start: %w", err)
	}

	return &CollectorManager{
		cmd:    cmd,
		config: cfg,
	}, nil
}

// Shutdown gracefully stops the collector process
func (m *CollectorManager) Shutdown() error {
	if m == nil || m.cmd == nil || m.cmd.Process == nil {
		return nil
	}

	// Try graceful shutdown with SIGTERM
	if err := m.cmd.Process.Signal(os.Interrupt); err != nil {
	}

	// Wait up to 5 seconds for graceful shutdown
	done := make(chan error, 1)
	go func() {
		done <- m.cmd.Wait()
	}()

	select {
	case <-time.After(5 * time.Second):
		// Force kill if not stopped
		if err := m.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill collector: %w", err)
		}
	case err := <-done:
		if err != nil && err.Error() != "signal: interrupt" {
		}
	}

	return nil
}

// isCollectorRunning checks if a collector is already listening on localhost:4317
func isCollectorRunning() bool {
	conn, err := net.DialTimeout("tcp", "localhost:4317", 1*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// waitForCollector waits for the collector to be ready to accept connections
func waitForCollector(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if isCollectorRunning() {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("collector did not start within %v", timeout)
}
