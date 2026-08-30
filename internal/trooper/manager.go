package trooper

import (
	"context"
	"fmt"
	"strings"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/jmoiron/sqlx"
)

// Manager handles trooper provisioning and lifecycle.
type Manager struct {
	sandboxMgr *sandbox.SandboxManager
	db         *sqlx.DB
}

// NewManager creates a new trooper manager.
func NewManager(sandboxMgr *sandbox.SandboxManager, db *sqlx.DB) *Manager {
	return &Manager{
		sandboxMgr: sandboxMgr,
		db:         db,
	}
}

// IdentityFiles holds the markdown identity documents for a trooper.
type IdentityFiles struct {
	SoulMD     string
	IdentityMD string
	UserMD     string
	RoleMD     string
}

// DatabaseConfig holds paths for trooper-local databases.
type DatabaseConfig struct {
	SqlitePath  string
	LanceDBPath string
	RedbPath    string
}

// ProvisionConfig holds all configuration needed to provision a trooper sandbox.
type ProvisionConfig struct {
	TrooperID      string
	TenantID       string
	Name           string
	Image          string
	CPULimit       float64
	MemoryMB       int64
	DiskMB         int64
	TimeoutSeconds int
	NetworkMode    string
	AllowedHosts   []string
	EnvVars        map[string]string
	SSHEnabled     bool
	GitRepoURL     string
	GitBranch      string
	BrowserSidecar *sandbox.BrowserSidecarConfig
	Identity       IdentityFiles
	Databases      DatabaseConfig
	// WorkDir is the workspace path inside the sandbox — typically
	// /workspace, but the agent composer's "Working Directory" field
	// can override it. Empty means "use the sandbox manager's default"
	// (which currently resolves to /workspace) so existing callers that
	// don't set this stay on the old behavior.
	WorkDir string
}

// Provision creates a persistent sandbox for a trooper, writes identity files, and initializes database directories.
func (m *Manager) Provision(ctx context.Context, cfg ProvisionConfig) (string, error) {
	if m.sandboxMgr == nil {
		return "", fmt.Errorf("sandbox manager not available")
	}

	logger.WithFields(
		"trooper_id", cfg.TrooperID,
		"tenant_id", cfg.TenantID,
		"image", cfg.Image,
	).Info("provisioning trooper sandbox")

	// Create a persistent sandbox keyed by trooper ID
	sandboxCfg := sandbox.SandboxConfig{
		Enabled:        true,
		Image:          cfg.Image,
		CPULimit:       cfg.CPULimit,
		MemoryMB:       cfg.MemoryMB,
		DiskMB:         cfg.DiskMB,
		TimeoutSeconds: cfg.TimeoutSeconds,
		NetworkMode:    cfg.NetworkMode,
		AllowedHosts:   cfg.AllowedHosts,
		EnvVars:        cfg.EnvVars,
		SSHEnabled:     cfg.SSHEnabled,
		GitRepoURL:     cfg.GitRepoURL,
		GitBranch:      cfg.GitBranch,
		BrowserSidecar: cfg.BrowserSidecar,
		Persistent:     true,
		Name:           cfg.Name,
		AgentID:        cfg.TrooperID,
		WorkDir:        cfg.WorkDir,
	}

	// Use trooper ID as session ID and provision through the persistent trooper path.
	sessionID := "trp-" + cfg.TrooperID
	inst, err := m.sandboxMgr.GetOrCreateTrooper(ctx, cfg.TrooperID, sessionID, cfg.TenantID, sandboxCfg)
	if err != nil {
		return "", fmt.Errorf("failed to create trooper sandbox: %w", err)
	}

	sandboxID := inst.ID

	// Write identity files
	if err := m.SyncIdentityFiles(ctx, sessionID, cfg.Identity); err != nil {
		logger.WithFields("trooper_id", cfg.TrooperID, "error", err.Error()).
			Warn("failed to sync identity files during provision")
	}

	// Initialize database directories
	if err := m.InitDatabases(ctx, sessionID, cfg.Databases); err != nil {
		logger.WithFields("trooper_id", cfg.TrooperID, "error", err.Error()).
			Warn("failed to init databases during provision")
	}

	// Update trooper row with sandbox_id
	if m.db != nil {
		_, err := m.db.ExecContext(ctx, `
			UPDATE troopers SET sandbox_id = $1, status = 'idle', updated_at = NOW()
			WHERE id = $2
		`, sandboxID, cfg.TrooperID)
		if err != nil {
			logger.WithFields("trooper_id", cfg.TrooperID, "error", err.Error()).
				Warn("failed to update trooper sandbox_id")
		}
	}

	return sandboxID, nil
}

// SyncIdentityFiles writes identity markdown files into the trooper sandbox.
func (m *Manager) SyncIdentityFiles(ctx context.Context, sessionID string, identity IdentityFiles) error {
	if m.sandboxMgr == nil {
		return fmt.Errorf("sandbox manager not available")
	}

	files := map[string]string{
		"/workspace/SOUL.md":     identity.SoulMD,
		"/workspace/IDENTITY.md": identity.IdentityMD,
		"/workspace/USER.md":     identity.UserMD,
		"/workspace/ROLE.md":     identity.RoleMD,
	}

	var errs []string
	for path, content := range files {
		if err := m.sandboxMgr.WriteFile(ctx, sessionID, path, []byte(content)); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", path, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to write identity files: %s", strings.Join(errs, "; "))
	}
	return nil
}

// InitDatabases creates the database directories within the sandbox.
func (m *Manager) InitDatabases(ctx context.Context, sessionID string, dbCfg DatabaseConfig) error {
	if m.sandboxMgr == nil {
		return fmt.Errorf("sandbox manager not available")
	}

	// Create parent directories for each database path
	paths := []string{dbCfg.SqlitePath, dbCfg.LanceDBPath, dbCfg.RedbPath}
	for _, p := range paths {
		if p == "" {
			continue
		}
		// Extract parent directory
		dir := p
		if idx := strings.LastIndex(p, "/"); idx > 0 {
			dir = p[:idx]
		}
		_, err := m.sandboxMgr.Exec(ctx, sessionID, sandbox.ExecRequest{
			Command: []string{"mkdir", "-p", dir},
		})
		if err != nil {
			return fmt.Errorf("failed to create dir %s: %w", dir, err)
		}
	}
	return nil
}

// Sleep stops a trooper's sandbox, preserving its state.
func (m *Manager) Sleep(ctx context.Context, trooperID string) error {
	if m.sandboxMgr == nil {
		return fmt.Errorf("sandbox manager not available")
	}

	// Look up sandbox_id from trooper
	var sandboxID string
	if m.db != nil {
		err := m.db.GetContext(ctx, &sandboxID, `
			SELECT sandbox_id FROM troopers WHERE id = $1 AND deleted_at IS NULL
		`, trooperID)
		if err != nil {
			return fmt.Errorf("trooper not found or has no sandbox: %w", err)
		}
	}

	if sandboxID == "" {
		return fmt.Errorf("trooper %s has no provisioned sandbox", trooperID)
	}

	if err := m.sandboxMgr.StopSandbox(ctx, sandboxID); err != nil {
		return fmt.Errorf("failed to stop trooper sandbox: %w", err)
	}

	// Update trooper status
	if m.db != nil {
		_, _ = m.db.ExecContext(ctx, `
			UPDATE troopers SET status = 'sleeping', updated_at = NOW() WHERE id = $1
		`, trooperID)
	}

	return nil
}

// Wake revives a sleeping trooper's sandbox.
func (m *Manager) Wake(ctx context.Context, trooperID string) error {
	if m.sandboxMgr == nil {
		return fmt.Errorf("sandbox manager not available")
	}

	var sandboxID string
	if m.db != nil {
		err := m.db.GetContext(ctx, &sandboxID, `
			SELECT sandbox_id FROM troopers WHERE id = $1 AND deleted_at IS NULL
		`, trooperID)
		if err != nil {
			return fmt.Errorf("trooper not found: %w", err)
		}
	}

	if sandboxID == "" {
		return fmt.Errorf("trooper %s has no provisioned sandbox", trooperID)
	}

	_, err := m.sandboxMgr.ReviveSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("failed to revive trooper sandbox: %w", err)
	}

	// Re-sync identity files
	if m.db != nil {
		var identity IdentityFiles
		err := m.db.GetContext(ctx, &identity, `
			SELECT soul_md, identity_md, user_md, role_md FROM troopers WHERE id = $1
		`, trooperID)
		if err == nil {
			sessionID := "trp-" + trooperID
			_ = m.SyncIdentityFiles(ctx, sessionID, identity)
		}

		_, _ = m.db.ExecContext(ctx, `
			UPDATE troopers SET status = 'idle', updated_at = NOW() WHERE id = $1
		`, trooperID)
	}

	return nil
}
