package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	ghpkg "github.com/everstacklabs/everstack/internal/github"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// RepoToolContext holds shared dependencies for GitHub repo browsing tools.
type RepoToolContext struct {
	App            *ghpkg.App
	InstallationID int64
	Owner          string
	Repo           string
	Branch         string
}

// NewRepoHandlers returns synthetic tool handlers for browsing the attached GitHub repo.
func NewRepoHandlers(ctx *RepoToolContext) []SyntheticToolHandler {
	return []SyntheticToolHandler{
		&repoGlobHandler{ctx: ctx},
		&repoReadFileHandler{ctx: ctx},
	}
}

// ---------------------------------------------------------------------------
// repo_glob — search files in the attached GitHub repo
// ---------------------------------------------------------------------------

type repoGlobHandler struct{ ctx *RepoToolContext }

func (h *repoGlobHandler) Name() string { return "repo_glob" }

func (h *repoGlobHandler) Definition() gw.ToolDefinition {
	desc := fmt.Sprintf(
		"Search for files in the attached GitHub repository (%s/%s, branch: %s) by glob pattern or substring. "+
			"Does NOT require a sandbox — queries the live repo via the GitHub API.",
		h.ctx.Owner, h.ctx.Repo, branchLabel(h.ctx.Branch),
	)
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "repo_glob",
			Description: desc,
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pattern": map[string]interface{}{
						"type":        "string",
						"description": "Glob pattern (e.g. '**/*.ts', 'src/**/*.go') or substring to match against file paths.",
					},
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Directory prefix to filter results (e.g. 'src/components'). Omit to search entire repo.",
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

func (h *repoGlobHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}

	filterPath, _ := args["path"].(string)
	filterPath = strings.TrimPrefix(filterPath, "/")
	filterPath = strings.TrimSuffix(filterPath, "/")

	maxResults := 100
	if m, ok := args["max_results"].(float64); ok && m > 0 {
		maxResults = int(m)
		if maxResults > 500 {
			maxResults = 500
		}
	}

	ref := h.ctx.Branch
	if ref == "" {
		ref = "HEAD"
	}

	tree, err := h.ctx.App.ListTree(ctx, h.ctx.InstallationID, h.ctx.Owner, h.ctx.Repo, ref)
	if err != nil {
		return "", fmt.Errorf("failed to fetch repo tree: %w", err)
	}

	var matched []repoFileEntry
	for _, entry := range tree {
		if entry.Type == "tree" {
			continue // skip directories, only return files
		}

		if filterPath != "" && !strings.HasPrefix(entry.Path, filterPath+"/") && entry.Path != filterPath {
			continue
		}

		if !matchGlobOrSubstring(entry.Path, pattern) {
			continue
		}

		matched = append(matched, repoFileEntry{
			Path: entry.Path,
			Size: entry.Size,
		})

		if len(matched) >= maxResults {
			break
		}
	}

	if len(matched) == 0 {
		return fmt.Sprintf("No files found matching %q in %s/%s", pattern, h.ctx.Owner, h.ctx.Repo), nil
	}

	result, _ := json.Marshal(map[string]interface{}{
		"repo":        fmt.Sprintf("%s/%s", h.ctx.Owner, h.ctx.Repo),
		"branch":      ref,
		"total_found": len(matched),
		"files":       matched,
	})
	return string(result), nil
}

// ---------------------------------------------------------------------------
// repo_read_file — read a file from the attached GitHub repo
// ---------------------------------------------------------------------------

type repoReadFileHandler struct{ ctx *RepoToolContext }

func (h *repoReadFileHandler) Name() string { return "repo_read_file" }

func (h *repoReadFileHandler) Definition() gw.ToolDefinition {
	desc := fmt.Sprintf(
		"Read the contents of a file from the attached GitHub repository (%s/%s, branch: %s). "+
			"Does NOT require a sandbox — fetches the file directly via the GitHub API. "+
			"Best for reading individual files without spinning up a full sandbox.",
		h.ctx.Owner, h.ctx.Repo, branchLabel(h.ctx.Branch),
	)
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "repo_read_file",
			Description: desc,
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the file within the repository (e.g. 'src/main.go', 'package.json').",
					},
				},
				"required": []string{"path"},
			},
		},
	}
}

func (h *repoReadFileHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	filePath, _ := args["path"].(string)
	if filePath == "" {
		return "", fmt.Errorf("path is required")
	}
	filePath = strings.TrimPrefix(filePath, "/")

	ref := h.ctx.Branch
	if ref == "" {
		ref = "HEAD"
	}

	content, err := h.ctx.App.GetFileContent(ctx, h.ctx.InstallationID, h.ctx.Owner, h.ctx.Repo, ref, filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file %q: %w", filePath, err)
	}

	return content, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

type repoFileEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

func branchLabel(branch string) string {
	if branch == "" {
		return "default"
	}
	return branch
}

// matchGlobOrSubstring matches a file path against a pattern. It supports:
// - Simple substring matching (e.g. "main.go" matches "src/main.go")
// - Basic glob patterns: *, **, ? with path-aware matching
func matchGlobOrSubstring(filePath, pattern string) bool {
	// If pattern has no glob chars, do case-insensitive substring match
	if !strings.ContainsAny(pattern, "*?[") {
		return strings.Contains(strings.ToLower(filePath), strings.ToLower(pattern))
	}

	// Convert glob to a matching function
	return globMatch(pattern, filePath)
}

// globMatch implements glob matching with ** (any path segments), * (any within segment), ? (single char).
func globMatch(pattern, name string) bool {
	// Handle ** patterns by splitting into segments
	patParts := splitGlobParts(pattern)
	nameParts := strings.Split(name, "/")
	return matchParts(patParts, nameParts)
}

type globPart struct {
	pattern  string
	doubleStar bool
}

func splitGlobParts(pattern string) []globPart {
	segments := strings.Split(pattern, "/")
	var parts []globPart
	for _, seg := range segments {
		if seg == "**" {
			parts = append(parts, globPart{doubleStar: true})
		} else {
			parts = append(parts, globPart{pattern: seg})
		}
	}
	return parts
}

func matchParts(patParts []globPart, nameParts []string) bool {
	if len(patParts) == 0 {
		return len(nameParts) == 0
	}

	p := patParts[0]
	rest := patParts[1:]

	if p.doubleStar {
		// ** matches zero or more path segments
		for i := 0; i <= len(nameParts); i++ {
			if matchParts(rest, nameParts[i:]) {
				return true
			}
		}
		return false
	}

	if len(nameParts) == 0 {
		return false
	}

	if !matchSegment(p.pattern, nameParts[0]) {
		return false
	}

	return matchParts(rest, nameParts[1:])
}

// matchSegment matches a single path segment against a glob pattern with * and ? wildcards.
func matchSegment(pattern, name string) bool {
	return matchSegmentHelper(pattern, name)
}

func matchSegmentHelper(pat, str string) bool {
	for len(pat) > 0 {
		switch pat[0] {
		case '*':
			// Skip consecutive *
			for len(pat) > 0 && pat[0] == '*' {
				pat = pat[1:]
			}
			if len(pat) == 0 {
				return true
			}
			for i := 0; i <= len(str); i++ {
				if matchSegmentHelper(pat, str[i:]) {
					return true
				}
			}
			return false
		case '?':
			if len(str) == 0 {
				return false
			}
			pat = pat[1:]
			str = str[1:]
		default:
			if len(str) == 0 || pat[0] != str[0] {
				return false
			}
			pat = pat[1:]
			str = str[1:]
		}
	}
	return len(str) == 0
}

