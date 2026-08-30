package sandbox

// Template defines a sandbox environment template.
// Templates are code-defined and shipped with the binary — not user-editable.
// The catalog is exposed via a read-only API so the frontend doesn't hardcode presets.
type Template struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Slug         string            `json:"slug"`
	Description  string            `json:"description"`
	Icon         string            `json:"icon"`
	IconColor    string            `json:"icon_color"`
	Image        string            `json:"image"`
	CPULimit     float64           `json:"cpu_limit"`
	MemoryMB     int64             `json:"memory_mb"`
	DiskMB       int64             `json:"disk_mb"`
	TimeoutSecs  int               `json:"timeout_seconds"`
	NetworkMode  string            `json:"network_mode"`
	AllowedHosts []string          `json:"allowed_hosts,omitempty"`
	EnvVars      map[string]string `json:"env_vars,omitempty"`
	WorkDir      string            `json:"work_dir"`
	Tags         []string          `json:"tags,omitempty"`
}

// TemplateCatalog is the platform-defined set of sandbox templates.
// Templates default to full outbound network access so sandboxes behave like
// normal development VMs. Use network_mode override for stricter isolation.
// Resource fields default to Nano, the smallest fixed managed size. Templates
// select software; callers choose a larger machine profile independently.
var TemplateCatalog = []Template{
	{
		ID:          "tpl_node",
		Name:        "Node.js",
		Slug:        "node",
		Description: "JavaScript/TypeScript — npm & yarn access with git/curl available. Use Vite for frontend apps.",
		Icon:        "simple-icons:nodedotjs",
		IconColor:   "#5FA04E",
		Image:       NodeImage,
		CPULimit:    0.5,
		MemoryMB:    512,
		DiskMB:      20480,
		TimeoutSecs: 300,
		NetworkMode: "allow",
		EnvVars: map[string]string{
			"NPM_CONFIG_PREFIX": "/workspace/.npm-global",
			"NPM_CONFIG_CACHE":  "/workspace/.npm-cache",
		},
		WorkDir: "/workspace",
		Tags:    []string{"javascript", "typescript", "runtime"},
	},
	{
		ID:          "tpl_deno",
		Name:        "Deno",
		Slug:        "deno",
		Description: "Secure TypeScript runtime — deno.land access",
		Icon:        "simple-icons:deno",
		IconColor:   "#70FFAF",
		Image:       DenoImage,
		CPULimit:    0.5,
		MemoryMB:    512,
		DiskMB:      20480,
		TimeoutSecs: 300,
		NetworkMode: "allow",
		WorkDir:     "/workspace",
		Tags:        []string{"typescript", "javascript", "runtime"},
	},
	{
		ID:          "tpl_python",
		Name:        "Python",
		Slug:        "python",
		Description: "Python 3 — pip & PyPI access",
		Icon:        "simple-icons:python",
		IconColor:   "#3776AB",
		Image:       PythonImage,
		CPULimit:    0.5,
		MemoryMB:    512,
		DiskMB:      20480,
		TimeoutSecs: 300,
		NetworkMode: "allow",
		EnvVars: map[string]string{
			"PIP_TARGET": "/workspace/.pip-packages",
		},
		WorkDir: "/workspace",
		Tags:    []string{"python", "runtime"},
	},
	{
		ID:          "tpl_go",
		Name:        "Go",
		Slug:        "go",
		Description: "Go — module proxy access",
		Icon:        "simple-icons:go",
		IconColor:   "#00ADD8",
		Image:       GoImage,
		CPULimit:    0.5,
		MemoryMB:    512,
		DiskMB:      20480,
		TimeoutSecs: 600,
		NetworkMode: "allow",
		EnvVars: map[string]string{
			"GOPATH": "/workspace/.go",
		},
		WorkDir: "/workspace",
		Tags:    []string{"go", "compiled"},
	},
	{
		ID:          "tpl_rust",
		Name:        "Rust",
		Slug:        "rust",
		Description: "Rust — crates.io & cargo access",
		Icon:        "simple-icons:rust",
		IconColor:   "#DEA584",
		Image:       RustImage,
		CPULimit:    0.5,
		MemoryMB:    512,
		DiskMB:      20480,
		TimeoutSecs: 600,
		NetworkMode: "allow",
		EnvVars: map[string]string{
			"CARGO_HOME": "/workspace/.cargo",
		},
		WorkDir: "/workspace",
		Tags:    []string{"rust", "compiled"},
	},
	{
		ID:          "tpl_ubuntu",
		Name:        "Ubuntu",
		Slug:        "ubuntu",
		Description: "General-purpose Linux — Node.js, Python, Go, Rust, Ruby pre-installed with package registry access",
		Icon:        "simple-icons:ubuntu",
		IconColor:   "#E95420",
		Image:       FullstackImage,
		CPULimit:    0.5,
		MemoryMB:    512,
		DiskMB:      20480,
		TimeoutSecs: 300,
		NetworkMode: "allow",
		WorkDir:     "/workspace",
		Tags:        []string{"linux", "general"},
	},
}

// templateIndex is a lookup map built once at init time.
var templateIndex map[string]*Template

func init() {
	templateIndex = make(map[string]*Template, len(TemplateCatalog)*2)
	for i := range TemplateCatalog {
		t := &TemplateCatalog[i]
		templateIndex[t.ID] = t
		templateIndex[t.Slug] = t // allow lookup by slug too
	}

	// Keep backward-compatible aliases for old dev slugs (e.g., "node-dev" → "node")
	aliases := map[string]string{
		"node-dev":       "node",
		"python-dev":     "python",
		"go-dev":         "go",
		"rust-dev":       "rust",
		"tpl_node_dev":   "node",
		"tpl_python_dev": "python",
		"tpl_go_dev":     "go",
		"tpl_rust_dev":   "rust",
	}
	for alias, slug := range aliases {
		if t, ok := templateIndex[slug]; ok {
			templateIndex[alias] = t
		}
	}
}

// GetTemplate returns a template by ID or slug, or nil if not found.
func GetTemplate(idOrSlug string) *Template {
	return templateIndex[idOrSlug]
}

// ListTemplates returns the full template catalog.
func ListTemplates() []Template {
	return TemplateCatalog
}

// TemplateToSandboxConfig converts a template into a SandboxConfig
// suitable for passing to SandboxManager.GetOrCreate.
func TemplateToSandboxConfig(t *Template) SandboxConfig {
	return SandboxConfig{
		Enabled:        true,
		Image:          t.Image,
		CPULimit:       t.CPULimit,
		MemoryMB:       t.MemoryMB,
		DiskMB:         t.DiskMB,
		TimeoutSeconds: t.TimeoutSecs,
		NetworkMode:    t.NetworkMode,
		AllowedHosts:   t.AllowedHosts,
		EnvVars:        t.EnvVars,
	}
}
