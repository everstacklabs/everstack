// Package agentproject loads and validates directory-based agent projects
// (the `evs init` / `evs deploy` format). A project compiles onto existing
// platform primitives: agent.yaml + instructions.md become an agent
// definition, project functions become revision-scoped callable exports,
// skills/*/SKILL.md become inline skills, triggers become agent triggers, and
// subagents/* become their own subagent-mode definitions linked to the parent.
//
//	my-agent/
//	  agent.yaml
//	  instructions.md
//	  tools/lookup_invoice.ts
//	  skills/refunds/SKILL.md
//	  subagents/risk-reviewer/agent.yaml
//	  subagents/risk-reviewer/instructions.md
package agentproject

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/everstacklabs/everstack/internal/agents/revision"
	"github.com/everstacklabs/everstack/internal/agents/trigger"
	"github.com/everstacklabs/everstack/internal/functions/isolation"
	"gopkg.in/yaml.v3"
)

// Config is the parsed agent.yaml.
type Config struct {
	Name         string                         `yaml:"name"`
	Description  string                         `yaml:"description"`
	Model        string                         `yaml:"model"`
	Instructions string                         `yaml:"instructions"` // path to markdown file, default ./instructions.md
	Limits       Limits                         `yaml:"limits"`
	Permissions  Permissions                    `yaml:"permissions"`
	Config       map[string]any                 `yaml:"config"`    // merged into AgentDefinition.config
	Tools        []string                       `yaml:"tools"`     // builtin names or ./tools/*.ts|js|py paths
	Functions    map[string]FunctionDeclaration `yaml:"functions"` // revision-local exported functions
	Files        []string                       `yaml:"files"`     // additional source files or directories bundled into the revision
	Skills       []string                       `yaml:"skills"`    // ./skills/<name> directories
	Subagents    []string                       `yaml:"subagents"` // ./subagents/<name> directories
	Triggers     []Trigger                      `yaml:"triggers"`
	Channels     []yaml.Node                    `yaml:"channels"` // parsed but not synced in the beta
}

// FunctionDeclaration exposes one source file export as an agent tool.
type FunctionDeclaration struct {
	File        string         `yaml:"file"`
	Export      string         `yaml:"export"`
	Description string         `yaml:"description"`
	Parameters  map[string]any `yaml:"parameters"`
}

type Limits struct {
	MaxTurns            int `yaml:"max_turns"`
	MaxToolCallsPerTurn int `yaml:"max_tool_calls_per_turn"`
	MaxSteps            int `yaml:"max_steps"`
}

type Permissions struct {
	TaskMode string `yaml:"task_mode"` // ask | always | deny
}

type Trigger struct {
	Type          string `yaml:"type" json:"type"` // cron | webhook | event
	Name          string `yaml:"name" json:"name"`
	Schedule      string `yaml:"schedule" json:"schedule,omitempty"` // cron expression
	Timezone      string `yaml:"timezone" json:"timezone,omitempty"`
	Input         string `yaml:"input" json:"input,omitempty"` // input template
	Event         string `yaml:"event" json:"event,omitempty"` // for type: event
	SourceAgentID string `yaml:"source_agent_id,omitempty" json:"source_agent_id,omitempty"`
	Enabled       *bool  `yaml:"enabled,omitempty" json:"enabled"`
}

// IsEnabled returns the effective trigger state. Trigger creation defaults to
// enabled, so an omitted enabled field in agent.yaml means true.
func (t Trigger) IsEnabled() bool {
	return t.Enabled == nil || *t.Enabled
}

// ToolFile is a local custom tool destined for the functions runtime.
type ToolFile struct {
	Name        string // function/tool name, derived from the file basename
	Path        string // path as written in agent.yaml
	Runtime     string // nodejs20 | deno | python3
	Code        string
	Export      string
	Description string
	// Parameters is the optional JSON-schema sidecar (<file>.params.json).
	Parameters map[string]any
}

// Skill is a local SKILL.md destined for inline agent skills config.
type Skill struct {
	Name        string
	Description string
	Content     string
}

// Project is a fully loaded, validated agent project.
type Project struct {
	Dir          string
	Config       Config
	Instructions string
	BuiltinTools []string
	ToolFiles    []ToolFile
	Skills       []Skill
	// RevisionManifest is the authoritative immutable source bundle. It owns
	// required project files, explicitly included source, and callable exports
	// for this agent only. Generated directories and credential files are
	// excluded.
	RevisionManifest *revision.Manifest
	// Subagents are nested projects declared via `subagents:`. Each deploys
	// as its own agent definition in subagent mode, linked to this one as a
	// subordinate. A subagent may not declare subagents of its own.
	Subagents []*Project
}

// EnsureRevisionManifest supplies the immutable source contract for Projects
// constructed programmatically. Projects loaded from disk already carry their
// complete source tree; this fallback preserves internal callers and tests.
func (p *Project) EnsureRevisionManifest() error {
	if p == nil {
		return fmt.Errorf("agent project is nil")
	}
	if p.RevisionManifest != nil {
		return nil
	}
	files := []revision.File{{Path: "instructions.md", Content: []byte(p.Instructions)}}
	functions := make([]revision.Function, 0, len(p.ToolFiles))
	seenPaths := map[string]struct{}{"instructions.md": {}}
	for _, tool := range p.ToolFiles {
		filePath := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(filepath.FromSlash(tool.Path))), "./")
		if filePath == "" || filePath == "." {
			filePath = "tools/" + tool.Name + runtimeExtension(tool.Runtime)
		}
		if _, exists := seenPaths[filePath]; !exists {
			files = append(files, revision.File{Path: filePath, Content: []byte(tool.Code)})
			seenPaths[filePath] = struct{}{}
		}
		functions = append(functions, revision.Function{
			Name: tool.Name, Description: tool.Description, Path: filePath,
			Export: tool.Export, Runtime: isolation.Runtime(tool.Runtime), Parameters: tool.Parameters,
		})
	}
	manifest, err := revision.NewManifest(files, functions)
	if err != nil {
		return err
	}
	p.RevisionManifest = manifest
	return nil
}

func runtimeExtension(runtime string) string {
	switch runtime {
	case "deno":
		return ".ts"
	case "nodejs20":
		return ".js"
	case "python3":
		return ".py"
	default:
		return ".txt"
	}
}

// maxSubagentDepth allows exactly one level of nesting: a root project may
// declare subagents, but those may not nest further. The platform links
// parent to child as a flat subordinate relation, so deeper trees would
// have no faithful representation on deploy or pull.
const maxSubagentDepth = 1

var toolNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)

// ValidateToolName validates a project-local callable name. Pull uses the same
// contract so it never exports a project that deploy cannot load again.
func ValidateToolName(name string) error {
	if !toolNameRe.MatchString(name) {
		return fmt.Errorf("name %q must match %s (lowercase snake_case)", name, toolNameRe)
	}
	return nil
}

// Load reads and validates the agent project rooted at dir.
func Load(dir string) (*Project, error) {
	p, err := load(dir, 0)
	if err != nil {
		return nil, err
	}
	if err := validateDeploymentAgentNames(p); err != nil {
		return nil, err
	}
	return p, nil
}

func load(dir string, depth int) (*Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}

	p := &Project{Dir: abs}
	agentYAML, err := lexicalProjectPath(abs, "agent.yaml")
	if err != nil {
		return nil, fmt.Errorf("agent.yaml: %w", err)
	}
	if _, err := os.Lstat(agentYAML); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no agent.yaml in %s (run `evs init` to scaffold a project)", dir)
		}
		return nil, err
	}
	agentYAML, err = p.resolve("agent.yaml")
	if err != nil {
		return nil, fmt.Errorf("agent.yaml: %w", err)
	}
	raw, err := os.ReadFile(agentYAML)
	if err != nil {
		return nil, err
	}

	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("agent.yaml: %w", err)
	}

	if strings.TrimSpace(cfg.Name) == "" {
		return nil, fmt.Errorf("agent.yaml: name is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("agent.yaml: model is required")
	}
	switch cfg.Permissions.TaskMode {
	case "":
		cfg.Permissions.TaskMode = "ask"
	case "ask", "always", "deny":
	default:
		return nil, fmt.Errorf("agent.yaml: permissions.task_mode must be ask, always or deny")
	}
	if _, reserved := cfg.Config[projectMetaKey]; reserved {
		return nil, fmt.Errorf("agent.yaml: config.%s is reserved for deployment metadata", projectMetaKey)
	}
	if _, err := json.Marshal(cfg.Config); err != nil {
		return nil, fmt.Errorf("agent.yaml: config must be JSON-compatible: %w", err)
	}
	for name, value := range map[string]int{
		"max_turns":               cfg.Limits.MaxTurns,
		"max_tool_calls_per_turn": cfg.Limits.MaxToolCallsPerTurn,
		"max_steps":               cfg.Limits.MaxSteps,
	} {
		if value < 0 || int64(value) > int64(1<<31-1) {
			return nil, fmt.Errorf("agent.yaml: limits.%s must be between 0 and %d", name, int64(1<<31-1))
		}
	}

	p.Config = cfg

	// Instructions markdown -> system prompt.
	insPath := cfg.Instructions
	if insPath == "" {
		insPath = "instructions.md"
	}
	resolvedInstructions, err := p.resolve(insPath)
	if err != nil {
		return nil, fmt.Errorf("instructions file %s: %w", insPath, err)
	}
	insBytes, err := os.ReadFile(resolvedInstructions)
	if err != nil {
		return nil, fmt.Errorf("instructions file %s: %w", insPath, err)
	}
	// An empty system prompt is valid in the API and dashboard. Keep the file
	// required so the project remains explicit, but preserve empty content for
	// legacy agents and prompt-free runtime projects.
	p.Instructions = strings.TrimSpace(string(insBytes))

	// Tools: local files vs builtin names.
	seen := map[string]struct{}{}
	for _, entry := range cfg.Tools {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if !strings.HasPrefix(entry, "./") && !strings.HasPrefix(entry, "tools/") {
			if _, dup := seen[entry]; dup {
				return nil, fmt.Errorf("duplicate tool name %q", entry)
			}
			seen[entry] = struct{}{}
			p.BuiltinTools = append(p.BuiltinTools, entry)
			continue
		}
		tf, err := p.loadToolFile(entry)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[tf.Name]; dup {
			return nil, fmt.Errorf("duplicate tool name %q", tf.Name)
		}
		seen[tf.Name] = struct{}{}
		p.ToolFiles = append(p.ToolFiles, *tf)
	}
	functionNames := make([]string, 0, len(cfg.Functions))
	for name := range cfg.Functions {
		functionNames = append(functionNames, name)
	}
	sort.Strings(functionNames)
	for _, name := range functionNames {
		if err := ValidateToolName(name); err != nil {
			return nil, fmt.Errorf("function %s: %w", name, err)
		}
		declaration := cfg.Functions[name]
		if strings.TrimSpace(declaration.File) == "" {
			return nil, fmt.Errorf("function %s: file is required", name)
		}
		tf, err := p.loadToolFile(declaration.File)
		if err != nil {
			return nil, fmt.Errorf("function %s: %w", name, err)
		}
		tf.Name = name
		tf.Export = strings.TrimSpace(declaration.Export)
		tf.Description = strings.TrimSpace(declaration.Description)
		if len(declaration.Parameters) > 0 {
			if len(tf.Parameters) > 0 {
				return nil, fmt.Errorf("function %s: parameters are declared both inline and in a sidecar", name)
			}
			if _, err := json.Marshal(declaration.Parameters); err != nil {
				return nil, fmt.Errorf("function %s: parameters must be JSON-compatible: %w", name, err)
			}
			tf.Parameters = declaration.Parameters
		}
		if _, dup := seen[tf.Name]; dup {
			return nil, fmt.Errorf("duplicate tool name %q", tf.Name)
		}
		seen[tf.Name] = struct{}{}
		p.ToolFiles = append(p.ToolFiles, *tf)
	}

	// Skills directories.
	seenSkills := map[string]struct{}{}
	for _, entry := range cfg.Skills {
		skill, err := p.loadSkill(entry)
		if err != nil {
			return nil, err
		}
		if _, dup := seenSkills[skill.Name]; dup {
			return nil, fmt.Errorf("duplicate skill name %q", skill.Name)
		}
		seenSkills[skill.Name] = struct{}{}
		p.Skills = append(p.Skills, *skill)
	}

	// Subagent directories: each is a full project in its own right.
	if len(cfg.Subagents) > 0 && depth >= maxSubagentDepth {
		return nil, fmt.Errorf("subagent %q may not declare subagents of its own", cfg.Name)
	}
	seenSub := map[string]struct{}{}
	for _, entry := range cfg.Subagents {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		subDir, err := p.resolve(entry)
		if err != nil {
			return nil, fmt.Errorf("subagent %s: %w", entry, err)
		}
		sub, err := load(subDir, depth+1)
		if err != nil {
			return nil, fmt.Errorf("subagent %s: %w", entry, err)
		}
		if sub.Config.Name == cfg.Name {
			return nil, fmt.Errorf("subagent %s: name %q collides with its parent", entry, sub.Config.Name)
		}
		if _, dup := seenSub[sub.Config.Name]; dup {
			return nil, fmt.Errorf("duplicate subagent name %q", sub.Config.Name)
		}
		seenSub[sub.Config.Name] = struct{}{}
		p.Subagents = append(p.Subagents, sub)
	}

	// Triggers.
	seenTriggers := map[string]struct{}{}
	for i, t := range cfg.Triggers {
		switch t.Type {
		case "cron":
			if err := trigger.ValidateCronConfiguration(t.Schedule, t.Timezone); err != nil {
				return nil, fmt.Errorf("triggers[%d]: %w", i, err)
			}
		case "webhook":
		case "event":
			if strings.TrimSpace(t.Event) == "" || strings.TrimSpace(t.SourceAgentID) == "" {
				return nil, fmt.Errorf("triggers[%d]: event trigger needs event and source_agent_id", i)
			}
		default:
			return nil, fmt.Errorf("triggers[%d]: type must be cron, webhook or event", i)
		}
		name := strings.TrimSpace(t.Name)
		if name == "" {
			name = fmt.Sprintf("%s-%s-%d", cfg.Name, t.Type, i+1)
		}
		if _, dup := seenTriggers[name]; dup {
			return nil, fmt.Errorf("triggers[%d]: duplicate effective trigger name %q", i, name)
		}
		seenTriggers[name] = struct{}{}
		cfg.Triggers[i].Name = name
		// CreateAgentTrigger normalizes an omitted timezone to UTC. Normalize
		// before hashing and comparison so an unchanged redeploy is a no-op.
		if strings.TrimSpace(t.Timezone) == "" {
			cfg.Triggers[i].Timezone = "UTC"
		}
		if t.Enabled == nil {
			enabled := true
			cfg.Triggers[i].Enabled = &enabled
		}
	}
	p.Config = cfg

	manifest, err := p.buildRevisionManifest()
	if err != nil {
		return nil, fmt.Errorf("agent revision: %w", err)
	}
	p.RevisionManifest = manifest

	return p, nil
}

// validateDeploymentAgentNames protects the tenant-global agent namespace.
// Duplicate graph names must fail before the deployment preflight can mutate
// the first of two definitions with the same name.
func validateDeploymentAgentNames(root *Project) error {
	owners := map[string]string{}
	var walk func(*Project) error
	walk = func(p *Project) error {
		if owner := owners[p.Config.Name]; owner != "" {
			return fmt.Errorf("agent name %q is declared by both %s and %s", p.Config.Name, owner, p.Dir)
		}
		owners[p.Config.Name] = p.Dir
		for _, sub := range p.Subagents {
			if err := walk(sub); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(root)
}

const projectMetaKey = "agentproject"

func (p *Project) resolve(rel string) (string, error) {
	candidate, err := lexicalProjectPath(p.Dir, rel)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	if !pathWithin(p.Dir, resolved) {
		return "", fmt.Errorf("path %q must stay within its project directory", rel)
	}
	return resolved, nil
}

func lexicalProjectPath(root, rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", fmt.Errorf("project path is empty")
	}
	native := filepath.FromSlash(rel)
	if filepath.IsAbs(native) || filepath.VolumeName(native) != "" {
		return "", fmt.Errorf("path %q must stay within its project directory", rel)
	}
	for _, part := range strings.Split(native, string(filepath.Separator)) {
		if part == ".." {
			return "", fmt.Errorf("path %q must stay within its project directory", rel)
		}
	}
	candidate := filepath.Join(root, filepath.Clean(native))
	if !pathWithin(root, candidate) {
		return "", fmt.Errorf("path %q must stay within its project directory", rel)
	}
	return candidate, nil
}

func pathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (p *Project) loadToolFile(entry string) (*ToolFile, error) {
	path, err := p.resolve(entry)
	if err != nil {
		return nil, fmt.Errorf("tool %s: %w", entry, err)
	}
	code, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("tool %s: %w", entry, err)
	}

	base := filepath.Base(path)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	if err := ValidateToolName(name); err != nil {
		return nil, fmt.Errorf("tool file %s: %w", entry, err)
	}

	var runtime string
	switch ext {
	case ".ts":
		runtime = "deno" // runs TypeScript natively, no build step
	case ".js", ".mjs":
		runtime = "nodejs20"
	case ".py":
		runtime = "python3"
	default:
		return nil, fmt.Errorf("tool file %s: unsupported extension %q (use .ts, .js or .py)", entry, ext)
	}

	rel, err := filepath.Rel(p.Dir, path)
	if err != nil || !pathWithin(p.Dir, path) {
		return nil, fmt.Errorf("tool %s: could not resolve project-relative path", entry)
	}
	tf := &ToolFile{
		Name:    name,
		Path:    filepath.ToSlash(rel),
		Runtime: runtime,
		Code:    string(code),
	}

	// Optional JSON-schema sidecar: tools/<name>.params.json
	sidecarEntry := strings.TrimSuffix(entry, filepath.Ext(entry)) + ".params.json"
	sidecar, err := lexicalProjectPath(p.Dir, sidecarEntry)
	if err != nil {
		return nil, fmt.Errorf("tool %s: params sidecar: %w", entry, err)
	}
	if _, err := os.Lstat(sidecar); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("tool %s: params sidecar: %w", entry, err)
		}
	} else {
		sidecar, err = p.resolve(sidecarEntry)
		if err != nil {
			return nil, fmt.Errorf("tool %s: params sidecar: %w", entry, err)
		}
		data, err := os.ReadFile(sidecar)
		if err != nil {
			return nil, fmt.Errorf("tool %s: params sidecar: %w", entry, err)
		}
		var params map[string]any
		if err := unmarshalJSON(data, &params); err != nil {
			return nil, fmt.Errorf("tool %s: invalid params sidecar %s: %w", entry, filepath.Base(sidecar), err)
		}
		tf.Parameters = params
	}
	return tf, nil
}

func (p *Project) buildRevisionManifest() (*revision.Manifest, error) {
	files, err := p.collectRevisionFiles()
	if err != nil {
		return nil, err
	}
	functions := make([]revision.Function, 0, len(p.ToolFiles))
	for _, tool := range p.ToolFiles {
		functions = append(functions, revision.Function{
			Name:        tool.Name,
			Description: tool.Description,
			Path:        tool.Path,
			Export:      tool.Export,
			Runtime:     isolation.Runtime(tool.Runtime),
			Parameters:  tool.Parameters,
		})
	}
	return revision.NewManifest(files, functions)
}

func (p *Project) collectRevisionFiles() ([]revision.File, error) {
	subagentRoots := make(map[string]struct{}, len(p.Config.Subagents))
	for _, declared := range p.Config.Subagents {
		normalized := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(filepath.FromSlash(declared))), "./")
		if normalized != "" && normalized != "." {
			subagentRoots[normalized] = struct{}{}
		}
	}
	collector := revisionFileCollector{
		root: p.Dir, subagentRoots: subagentRoots, files: make(map[string]revision.File),
	}

	instructionsPath := p.Config.Instructions
	if instructionsPath == "" {
		instructionsPath = "instructions.md"
	}
	required := []string{"agent.yaml", instructionsPath}
	for _, tool := range p.ToolFiles {
		required = append(required, tool.Path)
		sidecar := strings.TrimSuffix(tool.Path, filepath.Ext(tool.Path)) + ".params.json"
		candidate, err := lexicalProjectPath(p.Dir, sidecar)
		if err != nil {
			return nil, err
		}
		if _, err := os.Lstat(candidate); err == nil {
			required = append(required, sidecar)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	for _, skillPath := range p.Config.Skills {
		required = append(required, filepath.ToSlash(filepath.Join(skillPath, "SKILL.md")))
	}
	for _, filePath := range required {
		if err := collector.add(filePath, true); err != nil {
			return nil, err
		}
	}
	for _, filePath := range p.Config.Files {
		if err := collector.add(filePath, false); err != nil {
			return nil, fmt.Errorf("files entry %q: %w", filePath, err)
		}
	}

	paths := make([]string, 0, len(collector.files))
	for filePath := range collector.files {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	files := make([]revision.File, 0, len(paths))
	for _, filePath := range paths {
		files = append(files, collector.files[filePath])
	}
	return files, nil
}

type revisionFileCollector struct {
	root          string
	subagentRoots map[string]struct{}
	files         map[string]revision.File
}

func (c *revisionFileCollector) add(raw string, required bool) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("source path is empty")
	}
	candidate, relSlash, info, err := resolveProjectPathWithoutSymlinks(c.root, raw)
	if err != nil {
		return err
	}
	if c.inSubagent(relSlash) {
		return fmt.Errorf("source path %q belongs to a separately versioned subagent", raw)
	}
	parts := strings.Split(relSlash, "/")
	if relSlash != "." && (pathContainsExcludedProjectDirectory(parts) || excludedProjectFile(parts)) {
		if required {
			return fmt.Errorf("required source path %q is excluded because it may contain generated output or credentials", raw)
		}
		return fmt.Errorf("path is excluded because it may contain generated output or credentials")
	}
	if !info.IsDir() {
		return c.addFile(candidate, relSlash, info)
	}

	return filepath.WalkDir(candidate, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(c.root, filePath)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		parts := strings.Split(relSlash, "/")
		if entry.IsDir() && (c.inSubagent(relSlash) || excludedProjectDirectory(parts)) {
			return filepath.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source path %q is a symlink; revision files must be regular files", filepath.ToSlash(rel))
		}
		if entry.IsDir() || excludedProjectFile(parts) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return c.addFile(filePath, relSlash, info)
	})
}

func (c *revisionFileCollector) addFile(filePath, relSlash string, info fs.FileInfo) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source path %q is not a regular file", relSlash)
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	c.files[relSlash] = revision.File{
		Path: relSlash, Mode: int32(info.Mode().Perm()), Content: content,
	}
	return nil
}

func (c *revisionFileCollector) inSubagent(relSlash string) bool {
	for root := range c.subagentRoots {
		if relSlash == root || strings.HasPrefix(relSlash, root+"/") {
			return true
		}
	}
	return false
}

func resolveProjectPathWithoutSymlinks(root, raw string) (string, string, fs.FileInfo, error) {
	candidate, err := lexicalProjectPath(root, raw)
	if err != nil {
		return "", "", nil, err
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return "", "", nil, err
	}
	current := root
	if rel != "." {
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			info, err := os.Lstat(current)
			if err != nil {
				return "", "", nil, err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return "", "", nil, fmt.Errorf("source path %q is a symlink; revision files must be regular files", raw)
			}
		}
	}
	info, err := os.Lstat(candidate)
	if err != nil {
		return "", "", nil, err
	}
	return candidate, filepath.ToSlash(rel), info, nil
}

func pathContainsExcludedProjectDirectory(parts []string) bool {
	for i := range parts {
		if excludedProjectDirectory(parts[:i+1]) {
			return true
		}
	}
	return false
}

func excludedProjectDirectory(parts []string) bool {
	name := strings.ToLower(parts[len(parts)-1])
	if len(parts) == 1 && name == "subagents" {
		return true
	}
	if len(parts) >= 2 && strings.EqualFold(parts[len(parts)-2], ".config") && name == "gcloud" {
		return true
	}
	switch name {
	case ".git", ".hg", ".svn", ".everstack", "node_modules", ".venv", "venv", "__pycache__", ".next", ".turbo", ".terraform", "dist", "build",
		".aws", ".azure", ".gnupg", ".kube", ".ssh":
		return true
	default:
		return false
	}
}

func excludedProjectFile(parts []string) bool {
	name := strings.ToLower(parts[len(parts)-1])
	if name == ".ds_store" {
		return true
	}
	if name == ".env" || name == ".envrc" || strings.HasPrefix(name, ".env.") {
		switch name {
		case ".env.example", ".env.sample", ".env.template":
			return false
		default:
			return true
		}
	}
	switch name {
	case ".npmrc", ".netrc", ".pypirc", ".git-credentials", ".dockerconfigjson",
		"credentials", "credentials.json", "secret.json", "secrets.json",
		"secret.yaml", "secret.yml", "secrets.yaml", "secrets.yml",
		"terraform.tfstate", "terraform.tfstate.backup", "kubeconfig", "application_default_credentials.json",
		"service-account.json", "service_account.json", "id_rsa", "id_dsa",
		"id_ecdsa", "id_ed25519":
		return true
	}
	if len(parts) >= 2 && strings.EqualFold(parts[len(parts)-2], ".docker") && name == "config.json" {
		return true
	}
	for _, suffix := range []string{".pem", ".key", ".p12", ".pfx", ".jks", ".keystore", ".tfvars", ".tfstate", ".tfstate.backup"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func (p *Project) loadSkill(entry string) (*Skill, error) {
	dir, err := p.resolve(entry)
	if err != nil {
		return nil, fmt.Errorf("skill %s: %w", entry, err)
	}
	skillFile, err := p.resolve(filepath.ToSlash(filepath.Join(entry, "SKILL.md")))
	if err != nil {
		return nil, fmt.Errorf("skill %s: %w", entry, err)
	}
	content, err := os.ReadFile(skillFile)
	if err != nil {
		return nil, fmt.Errorf("skill %s: %w", entry, err)
	}

	name := filepath.Base(dir)
	description := ""
	body := string(content)

	// Optional YAML frontmatter: name + description override the directory name.
	if strings.HasPrefix(body, "---\n") {
		if end := strings.Index(body[4:], "\n---"); end >= 0 {
			var fm struct {
				Name        string `yaml:"name"`
				Description string `yaml:"description"`
			}
			if err := yaml.Unmarshal([]byte(body[4:4+end]), &fm); err == nil {
				if fm.Name != "" {
					name = fm.Name
				}
				description = fm.Description
			}
		}
	}

	return &Skill{Name: name, Description: description, Content: body}, nil
}

// Hash returns a stable digest of the compiled desired state; stored in the
// deployed agent's config so `evs deploy` can identify the exact local state
// that produced a deployment stamp. Raw source paths and channels are omitted:
// paths do not reach the API, and channels are not compiled by the CLI beta.
func (p *Project) Hash() string {
	builtins := append([]string(nil), p.BuiltinTools...)
	sort.Strings(builtins)

	type toolState struct {
		Name       string         `json:"name"`
		Runtime    string         `json:"runtime"`
		Code       string         `json:"code"`
		Parameters map[string]any `json:"parameters,omitempty"`
	}
	tools := make([]toolState, 0, len(p.ToolFiles))
	for _, tf := range p.ToolFiles {
		tool := toolState{Name: tf.Name, Runtime: tf.Runtime, Code: tf.Code}
		if len(tf.Parameters) > 0 {
			tool.Parameters = tf.Parameters
		}
		tools = append(tools, tool)
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })

	type skillState struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Content     string `json:"content"`
	}
	skills := make([]skillState, 0, len(p.Skills))
	for _, s := range p.Skills {
		skills = append(skills, skillState{Name: s.Name, Description: s.Description, Content: s.Content})
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })

	type subagentState struct {
		Name string `json:"name"`
		Hash string `json:"hash"`
	}
	subagents := make([]subagentState, 0, len(p.Subagents))
	for _, s := range p.Subagents {
		subagents = append(subagents, subagentState{Name: s.Config.Name, Hash: s.Hash()})
	}
	sort.Slice(subagents, func(i, j int) bool { return subagents[i].Name < subagents[j].Name })

	triggers := append([]Trigger(nil), p.Config.Triggers...)
	sort.Slice(triggers, func(i, j int) bool { return triggers[i].Name < triggers[j].Name })

	state := struct {
		Name         string          `json:"name"`
		Description  string          `json:"description,omitempty"`
		Model        string          `json:"model"`
		Instructions string          `json:"instructions"`
		Limits       Limits          `json:"limits"`
		Permissions  Permissions     `json:"permissions"`
		Config       map[string]any  `json:"config,omitempty"`
		BuiltinTools []string        `json:"builtin_tools,omitempty"`
		Tools        []toolState     `json:"tools,omitempty"`
		Skills       []skillState    `json:"skills,omitempty"`
		Triggers     []Trigger       `json:"triggers,omitempty"`
		Subagents    []subagentState `json:"subagents,omitempty"`
		Revision     string          `json:"revision,omitempty"`
	}{
		Name:         p.Config.Name,
		Description:  p.Config.Description,
		Model:        p.Config.Model,
		Instructions: p.Instructions,
		Limits:       p.Config.Limits,
		Permissions:  p.Config.Permissions,
		Config:       p.Config.Config,
		BuiltinTools: builtins,
		Tools:        tools,
		Skills:       skills,
		Triggers:     triggers,
		Subagents:    subagents,
	}
	if p.RevisionManifest != nil {
		state.Revision = p.RevisionManifest.Digest
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		// Loaded projects are validated as JSON-compatible, and tool sidecars
		// originate as JSON. Keep Hash total for programmatically constructed
		// Projects while still producing a deterministic failure digest.
		encoded = []byte("invalid-project-state:" + err.Error())
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
