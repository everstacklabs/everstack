package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)


const (
	maxZipSize     = 10 * 1024 * 1024 // 10MB
	maxSkillMDSize = 32 * 1024        // 32KB
)

// SkillSpec identifies a skill source on GitHub.
type SkillSpec struct {
	Owner     string `json:"owner"`
	Repo      string `json:"repo"`
	SkillName string `json:"skill_name,omitempty"` // empty = discover all skills in repo
}

// SkillDefinition holds the parsed content of a SKILL.md file.
type SkillDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Content     string `json:"content"`
	InstalledAt string `json:"installed_at"`
}

// SkillFrontmatter is the YAML frontmatter in a SKILL.md file.
type SkillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// ParseSkillSpec parses a spec string like "owner/repo" or "owner/repo/skill-name".
// It strips any "https://github.com/" prefix.
func ParseSkillSpec(spec string) (*SkillSpec, error) {
	spec = strings.TrimSpace(spec)
	spec = strings.TrimPrefix(spec, "https://github.com/")
	spec = strings.TrimPrefix(spec, "http://github.com/")
	spec = strings.TrimSuffix(spec, "/")

	parts := strings.Split(spec, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("invalid skill spec %q: expected owner/repo or owner/repo/skill-name", spec)
	}

	s := &SkillSpec{
		Owner: parts[0],
		Repo:  parts[1],
	}
	if len(parts) >= 3 && parts[2] != "" {
		s.SkillName = parts[2]
	}

	return s, nil
}

// FetchSkills discovers SKILL.md files in a GitHub repo and fetches their contents.
// Uses the Git Trees API to locate files without downloading the entire archive,
// then fetches each SKILL.md individually via the Contents API.
// Falls back to the zipball approach for repos where the tree API fails.
// If spec.SkillName is set, only the matching skill is returned.
func FetchSkills(ctx context.Context, spec SkillSpec) ([]SkillDefinition, error) {
	skills, err := fetchSkillsViaTree(ctx, spec)
	if err != nil {
		// Fallback to zipball for private repos or API issues
		skills, fallbackErr := fetchSkillsViaZipball(ctx, spec)
		if fallbackErr != nil {
			return nil, err // return original tree error
		}
		return skills, nil
	}
	return skills, nil
}

// fetchSkillsViaTree uses the GitHub Git Trees API (recursive) to find SKILL.md files,
// then fetches each one individually. This avoids downloading the entire repo archive.
func fetchSkillsViaTree(ctx context.Context, spec SkillSpec) ([]SkillDefinition, error) {
	// Get the recursive tree for the default branch
	treeURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/trees/HEAD?recursive=1", spec.Owner, spec.Repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, treeURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create tree request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Everstack-Skills-Fetcher/1.0")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch tree: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub tree API returned status %d", resp.StatusCode)
	}

	var tree struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			Size int64  `json:"size"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if err := decodeJSON(resp.Body, &tree); err != nil {
		return nil, fmt.Errorf("failed to parse tree: %w", err)
	}

	// Find all SKILL.md files in the tree
	type skillFile struct {
		path      string
		skillName string
	}
	var targets []skillFile
	for _, entry := range tree.Tree {
		if entry.Type != "blob" {
			continue
		}
		base := path.Base(entry.Path)
		if !strings.EqualFold(base, "SKILL.md") {
			continue
		}
		if entry.Size > int64(maxSkillMDSize) {
			continue
		}

		// Determine skill name from parent directory
		dir := path.Dir(entry.Path)
		skillName := path.Base(dir)
		if skillName == "." || skillName == "" {
			skillName = spec.Repo
		}

		// Filter by requested skill name
		if spec.SkillName != "" && skillName != spec.SkillName {
			continue
		}

		targets = append(targets, skillFile{path: entry.Path, skillName: skillName})
	}

	if len(targets) == 0 {
		target := fmt.Sprintf("%s/%s", spec.Owner, spec.Repo)
		if spec.SkillName != "" {
			target += "/" + spec.SkillName
		}
		return nil, fmt.Errorf("no SKILL.md found in %s", target)
	}

	source := fmt.Sprintf("%s/%s", spec.Owner, spec.Repo)
	now := time.Now().UTC().Format(time.RFC3339)

	var skills []SkillDefinition
	for _, sf := range targets {
		content, err := fetchFileContents(ctx, spec.Owner, spec.Repo, sf.path)
		if err != nil {
			continue
		}

		skillSource := source
		if sf.skillName != spec.Repo {
			skillSource = source + "/" + sf.skillName
		}

		def, err := parseSkillMD(content, skillSource)
		if err != nil {
			continue
		}
		if def.Name == "" {
			def.Name = sf.skillName
		}
		def.InstalledAt = now

		skills = append(skills, *def)
	}

	if len(skills) == 0 {
		target := source
		if spec.SkillName != "" {
			target = source + "/" + spec.SkillName
		}
		return nil, fmt.Errorf("no valid SKILL.md found in %s", target)
	}

	return skills, nil
}

// fetchFileContents fetches a single file's raw content from GitHub.
func fetchFileContents(ctx context.Context, owner, repo, filePath string) ([]byte, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, filePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.raw+json")
	req.Header.Set("User-Agent", "Everstack-Skills-Fetcher/1.0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub contents API returned status %d for %s", resp.StatusCode, filePath)
	}

	return io.ReadAll(io.LimitReader(resp.Body, int64(maxSkillMDSize)))
}

// fetchSkillsViaZipball is the legacy approach that downloads the full repo archive.
// Used as fallback when the tree API is unavailable (e.g. empty repos, API issues).
func fetchSkillsViaZipball(ctx context.Context, spec SkillSpec) ([]SkillDefinition, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/zipball", spec.Owner, spec.Repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Everstack-Skills-Fetcher/1.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch repo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d for %s/%s", resp.StatusCode, spec.Owner, spec.Repo)
	}

	// Read with size limit
	limited := io.LimitReader(resp.Body, maxZipSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	if len(data) > maxZipSize {
		return nil, fmt.Errorf("repository archive exceeds %dMB limit", maxZipSize/(1024*1024))
	}

	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("failed to read zip archive: %w", err)
	}

	source := fmt.Sprintf("%s/%s", spec.Owner, spec.Repo)
	now := time.Now().UTC().Format(time.RFC3339)

	var skills []SkillDefinition
	for _, f := range reader.File {
		if f.FileInfo().IsDir() {
			continue
		}
		base := path.Base(f.Name)
		if !strings.EqualFold(base, "SKILL.md") {
			continue
		}
		if f.UncompressedSize64 > maxSkillMDSize {
			continue
		}

		// Determine skill name from parent directory
		dir := path.Dir(f.Name)
		// The zip has a top-level directory like "owner-repo-sha/..."
		// Strip the first path component
		parts := strings.SplitN(dir, "/", 2)
		var skillDir string
		if len(parts) > 1 {
			skillDir = parts[1]
		} else {
			skillDir = parts[0]
		}
		// The skill name is the immediate parent directory name
		skillName := path.Base(skillDir)
		if skillName == "." || skillName == "" {
			// SKILL.md at repo root — use repo name
			skillName = spec.Repo
		}

		// If a specific skill was requested, skip non-matching
		if spec.SkillName != "" && skillName != spec.SkillName {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			continue
		}
		content, err := io.ReadAll(io.LimitReader(rc, maxSkillMDSize))
		rc.Close()
		if err != nil {
			continue
		}

		skillSource := source
		if skillName != spec.Repo {
			skillSource = source + "/" + skillName
		}

		def, err := parseSkillMD(content, skillSource)
		if err != nil {
			continue
		}
		if def.Name == "" {
			def.Name = skillName
		}
		def.InstalledAt = now

		skills = append(skills, *def)
	}

	if len(skills) == 0 {
		target := source
		if spec.SkillName != "" {
			target = source + "/" + spec.SkillName
		}
		return nil, fmt.Errorf("no SKILL.md found in %s", target)
	}

	return skills, nil
}

// RegistrySkill represents a skill from the skills.sh registry.
type RegistrySkill struct {
	Source   string `json:"source"`
	SkillID  string `json:"skillId"`
	Name     string `json:"name"`
	Installs uint64 `json:"installs"`
}

// RegistryBrowseResponse is the response from the skills.sh browse API.
type RegistryBrowseResponse struct {
	Skills  []RegistrySkill `json:"skills"`
	HasMore bool            `json:"hasMore"`
}

// RegistrySearchResponse is the response from the skills.sh search API.
type RegistrySearchResponse struct {
	Skills []RegistrySkill `json:"skills"`
	Query  string          `json:"query"`
	Count  int             `json:"count"`
}

// RegistryBrowse proxies browse requests to skills.sh leaderboard API.
// view is one of "all-time", "trending", "hot". page is 0-indexed.
func RegistryBrowse(ctx context.Context, view string, page int) (*RegistryBrowseResponse, error) {
	switch view {
	case "all-time", "trending", "hot":
	default:
		view = "all-time"
	}
	if page < 0 {
		page = 0
	}

	url := fmt.Sprintf("https://skills.sh/api/skills/%s/%d", view, page)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "Everstack-Skills/1.0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("skills.sh browse request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("skills.sh returned status %d", resp.StatusCode)
	}

	var result RegistryBrowseResponse
	if err := decodeJSON(resp.Body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse skills.sh response: %w", err)
	}
	return &result, nil
}

// RegistrySearch proxies search requests to skills.sh search API.
func RegistrySearch(ctx context.Context, query string, limit int) (*RegistrySearchResponse, error) {
	if len(query) < 2 {
		return &RegistrySearchResponse{Query: query}, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	url := fmt.Sprintf("https://skills.sh/api/search?q=%s&limit=%d", query, limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "Everstack-Skills/1.0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("skills.sh search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("skills.sh returned status %d", resp.StatusCode)
	}

	var result RegistrySearchResponse
	if err := decodeJSON(resp.Body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse skills.sh search response: %w", err)
	}
	return &result, nil
}

func decodeJSON(r io.Reader, v interface{}) error {
	data, err := io.ReadAll(io.LimitReader(r, 2*1024*1024)) // 2MB limit
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// parseSkillMD splits YAML frontmatter from markdown body.
func parseSkillMD(content []byte, source string) (*SkillDefinition, error) {
	text := string(content)
	text = strings.TrimSpace(text)

	var fm SkillFrontmatter
	body := text

	// Check for YAML frontmatter (between --- markers)
	if strings.HasPrefix(text, "---") {
		rest := text[3:]
		endIdx := strings.Index(rest, "\n---")
		if endIdx >= 0 {
			frontmatterYAML := rest[:endIdx]
			body = strings.TrimSpace(rest[endIdx+4:])
			if err := yaml.Unmarshal([]byte(frontmatterYAML), &fm); err != nil {
				// Non-fatal: use the whole text as body
				body = text
			}
		}
	}

	return &SkillDefinition{
		Name:        fm.Name,
		Description: fm.Description,
		Source:      source,
		Content:     body,
	}, nil
}
