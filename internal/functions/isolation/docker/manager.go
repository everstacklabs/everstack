package docker

import (
	"context"
	"fmt"
	"sync"

	"github.com/everstacklabs/everstack/internal/functions/isolation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// BackendManager manages multiple Docker backends for per-function host targeting.
// It holds a global (pre-initialized) backend and lazily creates backends for remote hosts.
type BackendManager struct {
	global  *Backend
	remotes map[string]*Backend
	mu      sync.RWMutex
}

// NewBackendManager creates a manager wrapping a global Docker backend.
func NewBackendManager(global *Backend) *BackendManager {
	return &BackendManager{
		global:  global,
		remotes: make(map[string]*Backend),
	}
}

// GetBackend returns the backend for the given Docker host.
// Empty or matching host returns the global backend.
// A different host creates and caches a new backend.
func (m *BackendManager) GetBackend(ctx context.Context, dockerHost string) (isolation.Backend, error) {
	// Empty host or same as global -> return global
	if dockerHost == "" || dockerHost == m.global.config.Host {
		return m.global, nil
	}

	// Check cache (read lock)
	m.mu.RLock()
	if b, ok := m.remotes[dockerHost]; ok {
		m.mu.RUnlock()
		return b, nil
	}
	m.mu.RUnlock()

	// Create new backend (write lock)
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if b, ok := m.remotes[dockerHost]; ok {
		return b, nil
	}

	cfg := DefaultConfig()
	cfg.Host = dockerHost
	cfg.AutoDetect = false
	cfg.Pool.Enabled = false // No pooling for remote backends

	b, err := NewWithHost(cfg, dockerHost)
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker backend for host %s: %w", dockerHost, err)
	}

	// Start the remote backend
	if err := b.Start(ctx); err != nil {
		logger.WithFields("host", dockerHost, "error", err.Error()).Warn("failed to start remote Docker backend")
		return nil, fmt.Errorf("failed to start Docker backend for host %s: %w", dockerHost, err)
	}

	m.remotes[dockerHost] = b
	logger.WithFields("host", dockerHost).Info("remote Docker backend created and started")
	return b, nil
}

// GlobalHost returns the configured global Docker host.
func (m *BackendManager) GlobalHost() string {
	return m.global.config.Host
}

// Stop stops all backends (global + cached remotes).
func (m *BackendManager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var lastErr error
	for host, b := range m.remotes {
		if err := b.Stop(ctx); err != nil {
			logger.WithFields("host", host, "error", err.Error()).Warn("failed to stop remote Docker backend")
			lastErr = err
		}
	}
	m.remotes = make(map[string]*Backend)
	return lastErr
}
