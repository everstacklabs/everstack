package sandbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/github"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// ErrUnsupportedBackend is returned when git import is attempted on a backend
// that does not advertise the git_import runner capability.
var ErrUnsupportedBackend = fmt.Errorf("git import is not supported by this sandbox runner")

// SetGitHubApp sets the GitHub App client for git operations (clone, pre-flight checks).
func (m *SandboxManager) SetGitHubApp(app *github.App) {
	m.githubApp = app
}

// dataDir returns the sandbox data directory, creating it if needed.
func (m *SandboxManager) dataDir() string {
	dir := m.globalConfig.DataDir
	if dir == "" {
		dir = "/var/lib/everstack/sandboxes"
	}
	return dir
}

// cloneRepo performs a shallow git clone using a helper Docker container.
// The clone runs as uid 1000:1000 (the sandbox user) so files have correct ownership.
// Token is passed via env var (never in URL or args). Returns the actual HEAD SHA and on-disk repo size.
func (m *SandboxManager) cloneRepo(ctx context.Context, sandboxID, tenantID string, config SandboxConfig) (repoHostPath string, commitSHA string, repoSizeBytes int64, err error) {
	if !CapabilitiesForBackend(m.backend).Features.GitImport {
		return "", "", 0, ErrUnsupportedBackend
	}
	if err := m.ensureDataDirWritable(); err != nil {
		return "", "", 0, fmt.Errorf("sandbox data dir unavailable: %w", err)
	}

	appClient, err := m.resolveGitHubApp(ctx, tenantID)
	if err != nil {
		return "", "", 0, err
	}

	// Parse owner/repo from URL
	owner, repo, parseErr := parseGitHubRepoURL(config.GitRepoURL)
	if parseErr != nil {
		return "", "", 0, parseErr
	}

	branch := config.GitBranch
	installationID := config.GitInstallationID

	// Get installation token
	token, err := appClient.GetInstallationToken(ctx, installationID)
	if err != nil {
		return "", "", 0, fmt.Errorf("failed to get installation token: %w", err)
	}

	// Pre-flight: validate repo exists and branch exists
	repoInfo, err := appClient.GetRepository(ctx, installationID, owner, repo)
	if err != nil {
		return "", "", 0, fmt.Errorf("pre-flight: %w", err)
	}

	// Default to repo's default branch if not specified
	if branch == "" {
		branch = repoInfo.DefaultBranch
	}

	if _, err := appClient.GetBranch(ctx, installationID, owner, repo, branch); err != nil {
		return "", "", 0, fmt.Errorf("pre-flight: %w", err)
	}

	// Pre-flight: check repo size against quota (heuristic from GitHub API)
	maxSizeKB := 500 * 1024 // 500MB default
	if repoInfo.Size > maxSizeKB {
		return "", "", 0, fmt.Errorf("repository size (%d KB) exceeds limit (%d KB)", repoInfo.Size, maxSizeKB)
	}

	// Create a clean host-side clone directory.
	// Clone retries may reuse the same sandbox ID, so stale partial checkouts
	// must be removed first to avoid checkout path conflicts.
	repoHostPath = filepath.Join(m.dataDir(), sandboxID, "repo")
	if err := os.RemoveAll(repoHostPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", "", 0, fmt.Errorf("failed to reset repo dir: %w", err)
	}
	if err := os.MkdirAll(repoHostPath, 0777); err != nil {
		return "", "", 0, fmt.Errorf("failed to create repo dir: %w", err)
	}
	// Ensure the helper container's uid (1000) can write to the bind mount.
	if err := os.Chmod(repoHostPath, 0777); err != nil {
		logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
			Warn("sandbox_manager: failed to chmod repo dir")
	}

	// Clone using a helper Docker container running as uid 1000:1000.
	// Token passed via EVS_GIT_TOKEN env var (not in URL or args).
	cloneURL := fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)

	cloneCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// Use docker run to clone with correct ownership.
	// Clone into container-local /tmp first, then copy into the bind mount; this
	// avoids checkout failures on bind-mounted destinations in some environments.
	// We use tar instead of cp -a for the copy step because cp -a (--preserve=all)
	// tries to preserve xattrs and security contexts, which can fail on bind mounts
	// when running as a non-root user — causing cascading "No such file or directory"
	// errors as cp removes partially-created directories it can't fully preserve.
	cloneScript := fmt.Sprintf(
		`set -eu
printf '#!/bin/sh\necho $EVS_GIT_TOKEN' > /tmp/.askpass
chmod 700 /tmp/.askpass
if [ -e /clone ] && [ ! -d /clone ]; then
  echo "/clone exists but is not a directory" >&2
  exit 1
fi
mkdir -p /clone
find /clone -mindepth 1 -maxdepth 1 -exec rm -rf {} +
rm -rf /tmp/repo
GIT_ASKPASS=/tmp/.askpass git clone --depth 1 --branch %s --single-branch %s /tmp/repo
(cd /tmp/repo && tar cf - .) | (cd /clone && tar xf -)
rm -f /tmp/.askpass`,
		shellEscape(branch), shellEscape(cloneURL),
	)

	cmd := exec.CommandContext(cloneCtx, "docker", "run", "--rm",
		"-v", repoHostPath+":/clone",
		"-e", "EVS_GIT_TOKEN",
		"-u", "1000:1000",
		DefaultBaseImage,
		"sh", "-c", cloneScript,
	)
	// Token is provided through process environment only (never argv).
	cmd.Env = append(os.Environ(), "EVS_GIT_TOKEN="+token)

	output, err := cmd.CombinedOutput()
	if err != nil {
		// Clean up on failure
		_ = os.RemoveAll(filepath.Join(m.dataDir(), sandboxID))
		return "", "", 0, fmt.Errorf("git clone failed: %w\noutput: %s", err, redactToken(string(output), token))
	}

	logger.WithFields("sandbox_id", sandboxID, "repo", config.GitRepoURL, "branch", branch).
		Debug("sandbox_manager: git clone completed")

	// Record actual HEAD SHA
	shaCmd := exec.CommandContext(ctx, "git", "-C", repoHostPath, "rev-parse", "HEAD")
	shaOutput, err := shaCmd.Output()
	if err != nil {
		_ = os.RemoveAll(filepath.Join(m.dataDir(), sandboxID))
		return "", "", 0, fmt.Errorf("failed to read HEAD SHA: %w", err)
	}
	commitSHA = strings.TrimSpace(string(shaOutput))
	if commitSHA == "" {
		_ = os.RemoveAll(filepath.Join(m.dataDir(), sandboxID))
		return "", "", 0, fmt.Errorf("empty HEAD SHA after clone")
	}

	// Post-clone hard limit based on actual on-disk bytes.
	actualSizeBytes, sizeErr := directorySizeBytes(repoHostPath)
	if sizeErr != nil {
		_ = os.RemoveAll(filepath.Join(m.dataDir(), sandboxID))
		return "", "", 0, fmt.Errorf("failed to measure cloned repo size: %w", sizeErr)
	}
	maxRepoMB := envInt("EVS_SANDBOX_GIT_MAX_REPO_MB", 500)
	maxRepoBytes := int64(maxRepoMB) * 1024 * 1024
	if maxRepoBytes > 0 && actualSizeBytes > maxRepoBytes {
		_ = os.RemoveAll(filepath.Join(m.dataDir(), sandboxID))
		return "", "", 0, fmt.Errorf("cloned repository size (%d bytes) exceeds hard limit (%d bytes)", actualSizeBytes, maxRepoBytes)
	}

	// Per-tenant aggregate clone quota.
	if m.db != nil {
		tenantQuotaMB := envInt("EVS_SANDBOX_GIT_TENANT_QUOTA_MB", 10*1024)
		tenantQuotaBytes := int64(tenantQuotaMB) * 1024 * 1024
		if reserveErr := m.reserveTenantStorage(ctx, tenantID, "repo_clone", actualSizeBytes, tenantQuotaBytes); reserveErr != nil {
			_ = os.RemoveAll(filepath.Join(m.dataDir(), sandboxID))
			return "", "", 0, reserveErr
		}
	}

	// Ensure cloned files are readable by the sandbox user regardless of host uid/gid.
	chmodCmd := exec.CommandContext(ctx, "chmod", "-R", "a+rX", repoHostPath)
	if err := chmodCmd.Run(); err != nil {
		logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
			Warn("sandbox_manager: failed to set repo permissions")
	}

	// Persist git info to DB
	if m.db != nil {
		const q = `
			UPDATE sandbox_instances
			SET git_repo_url = $2, git_branch = $3, git_commit_sha = $4,
			    git_installation_id = $5, git_cloned_at = NOW()
			WHERE id = $1`
		_, _ = m.db.ExecContext(ctx, q, sandboxID, config.GitRepoURL, branch, commitSHA, installationID)
	}

	return repoHostPath, commitSHA, actualSizeBytes, nil
}

// cleanupSandboxData removes the entire sandbox data directory (repo clone + snapshots).
func (m *SandboxManager) cleanupSandboxData(sandboxID string) {
	dir := filepath.Join(m.dataDir(), sandboxID)
	repoDir := filepath.Join(dir, "repo")
	repoSizeBytes, _ := directorySizeBytes(repoDir)
	tenantID := ""
	if m.db != nil {
		_ = m.db.QueryRowxContext(context.Background(),
			`SELECT tenant_id FROM sandbox_instances WHERE id = $1`, sandboxID).Scan(&tenantID)
	}
	if err := os.RemoveAll(dir); err != nil {
		logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
			Warn("sandbox_manager: failed to cleanup sandbox data dir")
	}
	if tenantID != "" && repoSizeBytes > 0 {
		if err := m.releaseTenantStorage(context.Background(), tenantID, "repo_clone", repoSizeBytes); err != nil {
			logger.WithFields("sandbox_id", sandboxID, "tenant_id", tenantID, "error", err.Error()).
				Warn("sandbox_manager: failed to release repo clone storage usage")
		}
	}
}

// parseGitHubRepoURL extracts owner and repo from a GitHub URL or "owner/repo" string.
func parseGitHubRepoURL(repoURL string) (owner, repo string, err error) {
	// Handle "owner/repo" format
	if !strings.Contains(repoURL, "://") && strings.Count(repoURL, "/") == 1 {
		parts := strings.SplitN(repoURL, "/", 2)
		return parts[0], parts[1], nil
	}

	// Handle full URL: https://github.com/owner/repo or https://github.com/owner/repo.git
	repoURL = strings.TrimSuffix(repoURL, ".git")
	parts := strings.Split(repoURL, "/")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("invalid repo URL: %s", repoURL)
	}
	return parts[len(parts)-2], parts[len(parts)-1], nil
}

// shellEscape escapes a string for safe use in a shell command.
func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// redactToken replaces a token value with "[REDACTED]" in a string.
func redactToken(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "[REDACTED]")
}

func (m *SandboxManager) resolveGitHubApp(ctx context.Context, tenantID string) (*github.App, error) {
	if m.githubApp != nil {
		return m.githubApp, nil
	}
	if m.db == nil {
		return nil, fmt.Errorf("github app is not configured")
	}
	store := github.NewStore(m.db)
	appClient, _, err := store.LoadAppClientForTenant(ctx, tenantID)
	if err != nil {
		if errors.Is(err, github.ErrGitHubAppNotFound) {
			return nil, fmt.Errorf("github app is not connected for tenant %s", tenantID)
		}
		return nil, fmt.Errorf("failed to load github app credentials: %w", err)
	}
	return appClient, nil
}

func (m *SandboxManager) ensureDataDirWritable() error {
	dir := m.dataDir()

	ensureDir := func(path string) error {
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if mkErr := os.MkdirAll(path, 0755); mkErr != nil {
					return mkErr
				}
				info, err = os.Stat(path)
				if err != nil {
					return err
				}
			} else {
				return err
			}
		}
		if !info.IsDir() {
			return fmt.Errorf("sandbox data dir is not a directory: %s", path)
		}
		f, err := os.CreateTemp(path, ".mf-writecheck-*")
		if err != nil {
			return err
		}
		name := f.Name()
		_ = f.Close()
		_ = os.Remove(name)
		return nil
	}

	if err := ensureDir(dir); err == nil {
		return nil
	} else if dir != "/var/lib/everstack/sandboxes" {
		return fmt.Errorf("sandbox data dir unavailable (%s): %w. Set EVS_SANDBOX_DATA_DIR to a writable directory", dir, err)
	}

	// Dev-friendly fallback when the default /var/lib path is not writable.
	fallback := filepath.Join(os.TempDir(), "everstack", "sandboxes")
	if err := ensureDir(fallback); err != nil {
		return fmt.Errorf("sandbox data dir unavailable (%s) and fallback failed (%s): %w. Set EVS_SANDBOX_DATA_DIR to a writable directory", dir, fallback, err)
	}

	logger.WithFields("from", dir, "to", fallback).
		Warn("sandbox_manager: default sandbox data dir unavailable, using temp fallback")
	m.globalConfig.DataDir = fallback
	return nil
}

func directorySizeBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	return total, err
}

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func envBool(name string, fallback bool) bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	if raw == "" {
		return fallback
	}
	switch raw {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func envPercent(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func (m *SandboxManager) reserveTenantStorage(ctx context.Context, tenantID, resourceType string, deltaBytes, limitBytes int64) error {
	if deltaBytes <= 0 || m.db == nil || tenantID == "" {
		return nil
	}
	const q = `
		INSERT INTO tenant_storage_usage (tenant_id, resource_type, total_bytes, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (tenant_id, resource_type) DO UPDATE
		SET total_bytes = tenant_storage_usage.total_bytes + EXCLUDED.total_bytes,
		    updated_at = NOW()
		WHERE tenant_storage_usage.total_bytes + EXCLUDED.total_bytes <= $4
		RETURNING total_bytes`
	var total int64
	err := m.db.QueryRowxContext(ctx, q, tenantID, resourceType, deltaBytes, limitBytes).Scan(&total)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("tenant clone quota exceeded for %s", tenantID)
		}
		return fmt.Errorf("failed to reserve tenant storage: %w", err)
	}
	return nil
}

func (m *SandboxManager) releaseTenantStorage(ctx context.Context, tenantID, resourceType string, deltaBytes int64) error {
	if deltaBytes <= 0 || m.db == nil || tenantID == "" {
		return nil
	}
	const q = `
		UPDATE tenant_storage_usage
		SET total_bytes = GREATEST(total_bytes - $2, 0),
		    updated_at = NOW()
		WHERE resource_type = $1`
	if _, err := m.db.ExecContext(ctx, q, resourceType, deltaBytes); err != nil {
		return err
	}
	return nil
}
