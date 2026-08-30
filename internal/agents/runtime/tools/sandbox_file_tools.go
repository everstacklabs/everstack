package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

// defaultSearchExclusions lists directories excluded from grep/glob searches.
var defaultSearchExclusions = []string{
	"node_modules",
	".git",
	"__pycache__",
	"vendor",
	"dist",
	".next",
}

func isPathWithinBase(path, base string) bool {
	if path == base {
		return true
	}
	return strings.HasPrefix(path, base+"/")
}

func defaultWorkingDirectory(allowedWorkingDirectory string) string {
	if strings.TrimSpace(allowedWorkingDirectory) == "" {
		return "/workspace"
	}
	clean := filepath.Clean(strings.TrimSpace(allowedWorkingDirectory))
	if strings.HasPrefix(clean, "/workspace") || strings.HasPrefix(clean, "/tmp") || strings.HasPrefix(clean, "/repo") {
		return clean
	}
	return "/workspace"
}

func validatePathWithinConstraint(path, allowedWorkingDirectory string) string {
	if strings.TrimSpace(allowedWorkingDirectory) == "" {
		return ""
	}
	cleanPath := filepath.Clean(path)
	cleanBase := filepath.Clean(strings.TrimSpace(allowedWorkingDirectory))
	if !isPathWithinBase(cleanPath, cleanBase) {
		return fmt.Sprintf("path must be within configured working_directory %s", cleanBase)
	}
	return ""
}

func validateReadablePath(path string) string {
	if path == "" {
		return "path is required"
	}
	cleanPath := filepath.Clean(path)
	if !strings.HasPrefix(cleanPath, "/workspace") && !strings.HasPrefix(cleanPath, "/tmp") && !strings.HasPrefix(cleanPath, "/repo") {
		return "path must be under /workspace, /tmp, or /repo"
	}
	return ""
}

func validateReadablePathWithConstraint(path, allowedWorkingDirectory string) string {
	if errMsg := validateReadablePath(path); errMsg != "" {
		return errMsg
	}
	return validatePathWithinConstraint(path, allowedWorkingDirectory)
}

// validateWritablePath checks that a path is safe for writing in the sandbox.
// Returns an error string if the path is invalid, or empty string if ok.
func validateWritablePath(path string) string {
	if path == "" {
		return "path is required"
	}
	cleanPath := filepath.Clean(path)
	if strings.HasPrefix(cleanPath, "/repo") {
		return "/repo is a read-only mount and cannot be written to"
	}
	if !strings.HasPrefix(cleanPath, "/workspace") && !strings.HasPrefix(cleanPath, "/tmp") {
		return "path must be under /workspace or /tmp"
	}
	return ""
}

func validateWritablePathWithConstraint(path, allowedWorkingDirectory string) string {
	if errMsg := validateWritablePath(path); errMsg != "" {
		return errMsg
	}
	return validatePathWithinConstraint(path, allowedWorkingDirectory)
}

// sanitizeGlobPattern validates a glob pattern for safe shell execution.
// Returns an error if the pattern contains disallowed characters.
func sanitizeGlobPattern(pattern string) error {
	if pattern == "" {
		return fmt.Errorf("pattern is required")
	}
	// Allow only safe characters in glob patterns to prevent command injection.
	safe := regexp.MustCompile(`^[a-zA-Z0-9*?.\-_/\[\]{},]+$`)
	if !safe.MatchString(pattern) {
		return fmt.Errorf("pattern contains disallowed characters; only alphanumeric, *, ?, ., -, _, /, [, ], {, } are allowed")
	}
	return nil
}

// buildGrepCommand constructs the grep command and arguments for sandbox exec.
func buildGrepCommand(pattern, path, include string, contextLines, maxResults int) []string {
	args := []string{"grep", "-rn", "-E"}

	if include != "" {
		args = append(args, "--include="+include)
	}

	for _, excl := range defaultSearchExclusions {
		args = append(args, "--exclude-dir="+excl)
	}

	if contextLines > 0 {
		args = append(args, fmt.Sprintf("-C%d", contextLines))
	}

	if maxResults > 0 {
		args = append(args, fmt.Sprintf("-m%d", maxResults))
	}

	args = append(args, "--", pattern, path)
	return args
}

// buildFindCommand constructs a find command for glob matching in the sandbox.
func buildFindCommand(pattern, path string, maxResults int) []string {
	args := []string{"find", path}

	// Add exclusions for common directories
	for i, excl := range defaultSearchExclusions {
		if i > 0 {
			args = append(args, "-o")
		}
		args = append(args, "-name", excl, "-type", "d")
	}
	// Prune excluded directories and proceed with the search
	args = []string{"find", path}
	for _, excl := range defaultSearchExclusions {
		args = append(args, "-path", "*/"+excl, "-prune", "-o")
	}

	// Determine if this is a deep glob (**/) or a simple name glob
	if strings.Contains(pattern, "/") {
		// For path-based patterns like "src/**/*.ts", use -path
		// Strip leading **/ as find handles recursion naturally
		cleanPattern := strings.TrimPrefix(pattern, "**/")
		args = append(args, "-name", cleanPattern, "-type", "f", "-print")
	} else {
		// Simple name pattern like "*.ts"
		args = append(args, "-name", pattern, "-type", "f", "-print")
	}

	return args
}

// ============================================================================
// sandbox_edit — exact string replacement in files
// ============================================================================

type sandboxEditHandler struct {
	ctx *SandboxSessionContext
}

func (h *sandboxEditHandler) Name() string { return "sandbox_edit" }

func (h *sandboxEditHandler) wireEmitter(emitter *agentrt.Emitter) {
	h.ctx.Emitter = emitter
}

func (h *sandboxEditHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "sandbox_edit",
			Description: "Perform an exact string replacement in a file. The old_string must match exactly (including whitespace and indentation). Use this instead of sed or awk for precise file edits.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Absolute path to the file (e.g., /workspace/app.py).",
					},
					"old_string": map[string]interface{}{
						"type":        "string",
						"description": "The exact text to find in the file.",
					},
					"new_string": map[string]interface{}{
						"type":        "string",
						"description": "The replacement text. Use empty string to delete the matched text.",
					},
					"replace_all": map[string]interface{}{
						"type":        "boolean",
						"description": "Replace all occurrences (default: false). When false, the match must be unique.",
					},
				},
				"required": []string{"path", "old_string", "new_string"},
			},
		},
	}
}

func (h *sandboxEditHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	path, _ := args["path"].(string)
	oldString, _ := args["old_string"].(string)
	newString, _ := args["new_string"].(string)
	replaceAll, _ := args["replace_all"].(bool)

	if path == "" || oldString == "" {
		return "", fmt.Errorf("path and old_string are required")
	}

	if errMsg := validateWritablePathWithConstraint(path, h.ctx.AllowedWorkingDirectory); errMsg != "" {
		return "Error: " + errMsg, nil
	}

	inst, err := ensureSandbox(ctx, h.ctx)
	if err != nil {
		return fmt.Sprintf("Failed to create sandbox: %s", err.Error()), nil
	}
	if _, err := ensureRepoReadyIfNeeded(ctx, h.ctx, inst, pathRequiresRepo(path)); err != nil {
		return fmt.Sprintf("Failed to prepare repository: %s", err.Error()), nil
	}

	// Read the file
	data, err := h.ctx.ReadFileWithRecovery(ctx, path)
	if err != nil {
		return fmt.Sprintf("Failed to read file: %s", err.Error()), nil
	}

	content := string(data)

	// Check file size (5MB cap)
	if len(data) > 5*1024*1024 {
		return "Error: file exceeds 5MB size limit for sandbox_edit", nil
	}

	// Count occurrences
	count := strings.Count(content, oldString)
	if count == 0 {
		return "Error: old_string not found in file", nil
	}
	if count > 1 && !replaceAll {
		return fmt.Sprintf("Error: old_string found %d times in file. Use replace_all=true to replace all occurrences, or provide a more specific old_string.", count), nil
	}

	// Perform replacement
	var newContent string
	if replaceAll {
		newContent = strings.ReplaceAll(content, oldString, newString)
	} else {
		newContent = strings.Replace(content, oldString, newString, 1)
	}

	// Write back
	if err := h.ctx.WriteFileWithRecovery(ctx, path, []byte(newContent)); err != nil {
		return fmt.Sprintf("Failed to write file: %s", err.Error()), nil
	}

	if replaceAll {
		return fmt.Sprintf("Replaced %d occurrence(s) in %s", count, path), nil
	}
	return fmt.Sprintf("Replaced 1 occurrence in %s", path), nil
}

// ============================================================================
// sandbox_grep — regex content search
// ============================================================================

type sandboxGrepHandler struct {
	ctx *SandboxSessionContext
}

func (h *sandboxGrepHandler) Name() string { return "sandbox_grep" }

func (h *sandboxGrepHandler) wireEmitter(emitter *agentrt.Emitter) {
	h.ctx.Emitter = emitter
}

func (h *sandboxGrepHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "sandbox_grep",
			Description: "Search file contents using regex patterns. Returns matching lines with file paths and line numbers. Automatically excludes node_modules, .git, __pycache__, vendor, dist, and .next directories.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pattern": map[string]interface{}{
						"type":        "string",
						"description": "Regular expression pattern to search for.",
					},
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Directory or file to search in (default: /workspace).",
					},
					"include": map[string]interface{}{
						"type":        "string",
						"description": "File glob filter (e.g., '*.py', '*.ts').",
					},
					"context_lines": map[string]interface{}{
						"type":        "integer",
						"description": "Lines of context before and after each match (default: 0, max: 5).",
					},
					"max_results": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of matches to return (default: 50, max: 200).",
					},
				},
				"required": []string{"pattern"},
			},
		},
	}
}

func (h *sandboxGrepHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}

	// Validate regex before sending to sandbox for a clear error message
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Sprintf("Invalid regex pattern: %s", err.Error()), nil
	}

	path, _ := args["path"].(string)
	if path == "" {
		path = defaultWorkingDirectory(h.ctx.AllowedWorkingDirectory)
	}
	if errMsg := validateReadablePathWithConstraint(path, h.ctx.AllowedWorkingDirectory); errMsg != "" {
		return "Error: " + errMsg, nil
	}

	include, _ := args["include"].(string)

	contextLines := 0
	if c, ok := args["context_lines"].(float64); ok && c > 0 {
		contextLines = int(c)
		if contextLines > 5 {
			contextLines = 5
		}
	}

	maxResults := 50
	if m, ok := args["max_results"].(float64); ok && m > 0 {
		maxResults = int(m)
		if maxResults > 200 {
			maxResults = 200
		}
	}

	inst, err := ensureSandbox(ctx, h.ctx)
	if err != nil {
		return fmt.Sprintf("Failed to create sandbox: %s", err.Error()), nil
	}
	if _, err := ensureRepoReadyIfNeeded(ctx, h.ctx, inst, pathRequiresRepo(path)); err != nil {
		return fmt.Sprintf("Failed to prepare repository: %s", err.Error()), nil
	}

	cmd := buildGrepCommand(pattern, path, include, contextLines, maxResults)

	if h.ctx.Emitter != nil {
		h.ctx.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventSandboxExec,
			SessionID: h.ctx.SessionID,
			Timestamp: time.Now(),
			SandboxID: inst.ID,
			Data: map[string]interface{}{
				"tool": "sandbox_grep",
			},
		})
	}

	result, err := h.ctx.ExecWithRecovery(ctx, sandbox.ExecRequest{
		Command: cmd,
		Timeout: 15 * time.Second,
	})
	if err != nil {
		return "", fmt.Errorf("grep execution failed: %w", err)
	}

	if h.ctx.Emitter != nil {
		h.ctx.Emitter.Emit(agentrt.Event{
			Type:              agentrt.EventSandboxResult,
			SessionID:         h.ctx.SessionID,
			Timestamp:         time.Now(),
			SandboxID:         inst.ID,
			SandboxExitCode:   result.ExitCode,
			SandboxDurationMs: result.DurationMs,
		})
	}

	// grep exit code 1 means no matches (not an error)
	if result.ExitCode == 1 && strings.TrimSpace(result.Stdout) == "" {
		return fmt.Sprintf("No matches found for pattern %q in %s", pattern, path), nil
	}

	if result.TimedOut {
		return "Error: grep timed out after 15 seconds. Try narrowing the search path or pattern.", nil
	}
	if result.ExitCode > 1 {
		return fmt.Sprintf("Error: grep failed (exit %d): %s", result.ExitCode, result.Stderr), nil
	}

	output := result.Stdout
	if len(output) > 100000 {
		output = output[:100000] + "\n... (output truncated)"
	}

	return output, nil
}

// ============================================================================
// sandbox_glob — file pattern matching
// ============================================================================

type sandboxGlobHandler struct {
	ctx *SandboxSessionContext
}

func (h *sandboxGlobHandler) Name() string { return "sandbox_glob" }

func (h *sandboxGlobHandler) wireEmitter(emitter *agentrt.Emitter) {
	h.ctx.Emitter = emitter
}

func (h *sandboxGlobHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "sandbox_glob",
			Description: "Find files matching a glob pattern. Returns file paths sorted alphabetically. Automatically excludes node_modules, .git, __pycache__, vendor, dist, and .next directories.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pattern": map[string]interface{}{
						"type":        "string",
						"description": "Glob pattern to match (e.g., '**/*.ts', '*.config.js', 'src/**/*.py').",
					},
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Root directory to search from (default: /workspace).",
					},
					"max_results": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of files to return (default: 100, max: 500).",
					},
				},
				"required": []string{"pattern"},
			},
		},
	}
}

func (h *sandboxGlobHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	pattern, _ := args["pattern"].(string)
	if err := sanitizeGlobPattern(pattern); err != nil {
		return fmt.Sprintf("Error: %s", err.Error()), nil
	}

	path, _ := args["path"].(string)
	if path == "" {
		path = defaultWorkingDirectory(h.ctx.AllowedWorkingDirectory)
	}
	if errMsg := validateReadablePathWithConstraint(path, h.ctx.AllowedWorkingDirectory); errMsg != "" {
		return "Error: " + errMsg, nil
	}

	maxResults := 100
	if m, ok := args["max_results"].(float64); ok && m > 0 {
		maxResults = int(m)
		if maxResults > 500 {
			maxResults = 500
		}
	}

	inst, err := ensureSandbox(ctx, h.ctx)
	if err != nil {
		return fmt.Sprintf("Failed to create sandbox: %s", err.Error()), nil
	}
	if _, err := ensureRepoReadyIfNeeded(ctx, h.ctx, inst, pathRequiresRepo(path)); err != nil {
		return fmt.Sprintf("Failed to prepare repository: %s", err.Error()), nil
	}

	cmd := buildFindCommand(pattern, path, maxResults)

	if h.ctx.Emitter != nil {
		h.ctx.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventSandboxExec,
			SessionID: h.ctx.SessionID,
			Timestamp: time.Now(),
			SandboxID: inst.ID,
			Data: map[string]interface{}{
				"tool": "sandbox_glob",
			},
		})
	}

	// Pipe through head to enforce max_results and sort for deterministic output
	shellCmd := fmt.Sprintf("%s | sort | head -n %d", shellJoin(cmd), maxResults)
	result, err := h.ctx.ExecWithRecovery(ctx, sandbox.ExecRequest{
		Command: []string{"sh", "-c", shellCmd},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		return "", fmt.Errorf("glob execution failed: %w", err)
	}

	if h.ctx.Emitter != nil {
		h.ctx.Emitter.Emit(agentrt.Event{
			Type:              agentrt.EventSandboxResult,
			SessionID:         h.ctx.SessionID,
			Timestamp:         time.Now(),
			SandboxID:         inst.ID,
			SandboxExitCode:   result.ExitCode,
			SandboxDurationMs: result.DurationMs,
		})
	}

	if result.TimedOut {
		return "Error: glob timed out after 10 seconds. Try a more specific pattern or path.", nil
	}

	output := strings.TrimSpace(result.Stdout)
	if output == "" {
		return fmt.Sprintf("No files found matching pattern %q in %s", pattern, path), nil
	}

	lines := strings.Split(output, "\n")
	return fmt.Sprintf("Found %d file(s):\n%s", len(lines), output), nil
}

// shellJoin quotes each argument for safe shell interpolation.
func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		// Single-quote each argument, escaping any embedded single quotes
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'"'"'`) + "'"
	}
	return strings.Join(quoted, " ")
}

// ============================================================================
// sandbox_patch — apply unified diffs
// ============================================================================

type sandboxPatchHandler struct {
	ctx *SandboxSessionContext
}

func (h *sandboxPatchHandler) Name() string { return "sandbox_patch" }

func (h *sandboxPatchHandler) wireEmitter(emitter *agentrt.Emitter) {
	h.ctx.Emitter = emitter
}

func (h *sandboxPatchHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "sandbox_patch",
			Description: "Apply a unified diff (patch) to files in the sandbox. Use this for multi-file or multi-hunk changes that would be tedious with sandbox_edit.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"patch": map[string]interface{}{
						"type":        "string",
						"description": "Unified diff content to apply.",
					},
					"working_directory": map[string]interface{}{
						"type":        "string",
						"description": "Base directory for applying the patch (default: /workspace).",
					},
					"strip": map[string]interface{}{
						"type":        "integer",
						"description": "Number of leading path components to strip (-p flag, default: 1).",
					},
				},
				"required": []string{"patch"},
			},
		},
	}
}

func (h *sandboxPatchHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	patchContent, _ := args["patch"].(string)
	if patchContent == "" {
		return "", fmt.Errorf("patch content is required")
	}

	workDir, _ := args["working_directory"].(string)
	if workDir == "" {
		workDir = defaultWorkingDirectory(h.ctx.AllowedWorkingDirectory)
	}

	if errMsg := validateWritablePathWithConstraint(workDir, h.ctx.AllowedWorkingDirectory); errMsg != "" {
		return "Error: " + errMsg, nil
	}

	strip := 1
	if s, ok := args["strip"].(float64); ok && s >= 0 {
		strip = int(s)
	}

	inst, err := ensureSandbox(ctx, h.ctx)
	if err != nil {
		return fmt.Sprintf("Failed to create sandbox: %s", err.Error()), nil
	}
	if _, err := ensureRepoReadyIfNeeded(ctx, h.ctx, inst, pathRequiresRepo(workDir)); err != nil {
		return fmt.Sprintf("Failed to prepare repository: %s", err.Error()), nil
	}

	// Write patch to temp file
	patchPath := "/tmp/.sandbox_patch.diff"
	if err := h.ctx.WriteFileWithRecovery(ctx, patchPath, []byte(patchContent)); err != nil {
		return fmt.Sprintf("Failed to write patch file: %s", err.Error()), nil
	}

	if h.ctx.Emitter != nil {
		h.ctx.Emitter.Emit(agentrt.Event{
			Type:      agentrt.EventSandboxExec,
			SessionID: h.ctx.SessionID,
			Timestamp: time.Now(),
			SandboxID: inst.ID,
			Data: map[string]interface{}{
				"tool": "sandbox_patch",
			},
		})
	}

	cmd := fmt.Sprintf("patch -p%d --batch < %s", strip, patchPath)
	result, err := h.ctx.ExecWithRecovery(ctx, sandbox.ExecRequest{
		Command: []string{"sh", "-c", cmd},
		WorkDir: workDir,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return "", fmt.Errorf("patch execution failed: %w", err)
	}

	if h.ctx.Emitter != nil {
		h.ctx.Emitter.Emit(agentrt.Event{
			Type:              agentrt.EventSandboxResult,
			SessionID:         h.ctx.SessionID,
			Timestamp:         time.Now(),
			SandboxID:         inst.ID,
			SandboxExitCode:   result.ExitCode,
			SandboxDurationMs: result.DurationMs,
		})
	}

	// Cleanup temp file (best-effort)
	_, _ = h.ctx.Manager.Exec(ctx, h.ctx.SessionID, sandbox.ExecRequest{
		Command: []string{"rm", "-f", patchPath},
		Timeout: 5 * time.Second,
	})

	if result.TimedOut {
		return "Error: patch timed out after 30 seconds.", nil
	}
	if result.ExitCode != 0 {
		return fmt.Sprintf("Patch failed (exit %d):\n%s\n%s", result.ExitCode, result.Stdout, result.Stderr), nil
	}

	output := strings.TrimSpace(result.Stdout)
	if output == "" {
		output = "Patch applied successfully"
	}
	return output, nil
}
