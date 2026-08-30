package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

// ============================================================================
// sandbox_git_clone — clone a GitHub repository into the sandbox
// ============================================================================

type sandboxGitCloneHandler struct {
	ctx *SandboxSessionContext
}

func (h *sandboxGitCloneHandler) Name() string { return "sandbox_git_clone" }

func (h *sandboxGitCloneHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "sandbox_git_clone",
			Description: "Clone a GitHub repository into the sandbox. If the sandbox is not yet created, configures the repo to be mounted at /repo on creation. If the sandbox already exists, clones the repo into /workspace via git.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"repo": map[string]interface{}{
						"type":        "string",
						"description": "GitHub repository in owner/repo format",
					},
					"branch": map[string]interface{}{
						"type":        "string",
						"description": "Branch to clone (defaults to repo's default branch)",
					},
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Directory to clone into (default: /workspace/<repo-name>). Only used when sandbox already exists.",
					},
				},
				"required": []string{"repo"},
			},
		},
	}
}

func (h *sandboxGitCloneHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	repoStr, _ := args["repo"].(string)
	if repoStr == "" {
		return "Error: repo is required (format: owner/repo)", nil
	}

	branch, _ := args["branch"].(string)
	clonePath, _ := args["path"].(string)

	// Validate repo format
	parts := strings.SplitN(repoStr, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "Error: repo must be in owner/repo format", nil
	}

	if h.ctx.Manager == nil {
		return "Error: sandbox manager not available", nil
	}

	// If sandbox already exists, clone directly via git command inside it
	if inst, ok := h.ctx.Manager.GetInstance(h.ctx.SessionID); ok {
		return h.cloneIntoExistingSandbox(ctx, inst.ID, repoStr, branch, clonePath)
	}

	// If the session already has a preconfigured repo (e.g., from agent config),
	// do not allow an LLM tool call to silently override it.
	existingRepo := strings.TrimSpace(h.ctx.Config.GitRepoURL)
	if existingRepo != "" {
		if !sameGitHubRepo(existingRepo, repoStr) {
			return fmt.Sprintf(
				"Git repo already preconfigured for this session: %s (branch: %s). Ignoring requested repo %s. Update sandbox.git_repo_url in the agent config or start a new session to change repositories.",
				existingRepo,
				branchOrDefault(strings.TrimSpace(h.ctx.Config.GitBranch)),
				repoStr,
			), nil
		}

		if strings.TrimSpace(branch) != "" {
			h.ctx.Config.GitBranch = branch
		}
		return fmt.Sprintf("Git clone already configured: %s (branch: %s). The repository will be mounted read-only at /repo when the sandbox is created.", existingRepo, branchOrDefault(strings.TrimSpace(h.ctx.Config.GitBranch))), nil
	}

	if h.ctx.Config.GitInstallationID <= 0 {
		// No GitHub app installation — sandbox not yet created, so we can't
		// clone via exec either. Fall back to configuring for public clone
		// on provisioning if possible, otherwise use exec after sandbox creation.
		return "Error: git_installation_id is not configured for this session; set sandbox.git_installation_id before calling sandbox_git_clone, or wait for the sandbox to be created and the repo will be cloned via git command", nil
	}

	h.ctx.Config.GitRepoURL = repoStr
	h.ctx.Config.GitBranch = branch

	return fmt.Sprintf("Git clone configured: %s (branch: %s). The repository will be mounted read-only at /repo when the sandbox is created.", repoStr, branchOrDefault(branch)), nil
}

// cloneIntoExistingSandbox runs `git clone` inside the running sandbox.
func (h *sandboxGitCloneHandler) cloneIntoExistingSandbox(ctx context.Context, sandboxID, repo, branch, clonePath string) (string, error) {
	repoURL := fmt.Sprintf("https://github.com/%s.git", repo)

	// Determine target directory
	if clonePath == "" {
		parts := strings.SplitN(repo, "/", 2)
		clonePath = fmt.Sprintf("/workspace/%s", parts[1])
	}

	// Build git clone command
	cmd := fmt.Sprintf("git clone --depth 1 %s %s", repoURL, clonePath)
	if branch != "" {
		cmd = fmt.Sprintf("git clone --depth 1 --branch %s %s %s", branch, repoURL, clonePath)
	}

	execCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	result, err := h.ctx.Manager.Exec(execCtx, h.ctx.SessionID, sandbox.ExecRequest{
		Command: []string{"sh", "-c", cmd},
		WorkDir: "/workspace",
		Timeout: 3 * time.Minute,
	})
	if err != nil {
		logger.WithFields("sandbox_id", sandboxID, "repo", repo, "error", err.Error()).
			Warn("sandbox_git_clone: exec failed")
		return fmt.Sprintf("Error cloning repository: %s", err.Error()), nil
	}

	output := strings.TrimSpace(result.Stdout + result.Stderr)
	if result.ExitCode != 0 {
		return fmt.Sprintf("Git clone failed (exit code %d):\n%s", result.ExitCode, output), nil
	}

	return fmt.Sprintf("Repository cloned to %s\n%s", clonePath, output), nil
}

func branchOrDefault(branch string) string {
	if branch == "" {
		return "default"
	}
	return branch
}

func sameGitHubRepo(a, b string) bool {
	return normalizeGitHubRepo(a) == normalizeGitHubRepo(b)
}

func normalizeGitHubRepo(repo string) string {
	s := strings.TrimSpace(repo)
	s = strings.TrimPrefix(s, "https://github.com/")
	s = strings.TrimPrefix(s, "http://github.com/")
	s = strings.TrimPrefix(s, "git@github.com:")
	s = strings.TrimPrefix(s, "github.com/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.Trim(s, "/")

	parts := strings.Split(s, "/")
	if len(parts) >= 2 {
		return strings.ToLower(parts[0] + "/" + parts[1])
	}
	return strings.ToLower(s)
}
