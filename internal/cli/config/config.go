package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	defaultAPIURL    = ""
	defaultOutput    = "table"
	defaultTransport = "grpc"
	configDir        = ".config/everstack"
	configFile       = "config.yaml"
)

// Config holds the full CLI config file contents.
type Config struct {
	ActiveContext string             `yaml:"active_context"`
	Contexts      map[string]Context `yaml:"contexts"`
}

// Context is a named profile (api endpoint, org, workspace, output format).
type Context struct {
	APIURL    string `yaml:"api_url,omitempty"`
	OrgSlug   string `yaml:"org,omitempty"`
	Workspace string `yaml:"workspace,omitempty"`
	Output    string `yaml:"output,omitempty"`
	Transport string `yaml:"transport,omitempty"`
}

// Resolved is a fully-resolved context after applying flag/env/profile/default precedence.
type Resolved struct {
	APIURL    string
	OrgSlug   string
	Workspace string
	Output    string
	Transport string
}

// Load reads the config file from disk. Returns an empty Config if the file does not exist.
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return defaultConfig(), nil
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return defaultConfig(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Contexts == nil {
		cfg.Contexts = map[string]Context{}
	}
	if cfg.ActiveContext == "" {
		cfg.ActiveContext = "default"
	}
	return &cfg, nil
}

// Save writes cfg to the config file atomically.
func Save(cfg *Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return os.Rename(tmp, path)
}

// Path returns the absolute path to the config file.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, configDir, configFile), nil
}

// ActiveCtx returns the Context for the active context name, or an empty Context if not found.
func (c *Config) ActiveCtx() Context {
	if c.Contexts == nil {
		return Context{}
	}
	return c.Contexts[c.ActiveContext]
}

// SetContext upserts a context by name.
func (c *Config) SetContext(name string, ctx Context) {
	if c.Contexts == nil {
		c.Contexts = map[string]Context{}
	}
	c.Contexts[name] = ctx
}

// Resolve builds a Resolved context from explicit overrides, then env vars, then the active
// context profile, then hardcoded defaults. Callers pass empty strings for values not overridden.
func Resolve(cfg *Config, flagAPIURL, flagOrg, flagWorkspace, flagOutput, flagTransport string) Resolved {
	active := Context{}
	if cfg != nil {
		active = cfg.ActiveCtx()
	}

	r := Resolved{
		APIURL:    coalesce(flagAPIURL, os.Getenv("EVS_API_URL"), active.APIURL, defaultAPIURL),
		OrgSlug:   coalesce(flagOrg, os.Getenv("EVS_ORG"), active.OrgSlug),
		Workspace: coalesce(flagWorkspace, os.Getenv("EVS_WORKSPACE"), active.Workspace),
		Output:    coalesce(flagOutput, os.Getenv("EVS_OUTPUT"), active.Output, defaultOutput),
		Transport: coalesce(flagTransport, os.Getenv("EVS_TRANSPORT"), active.Transport, defaultTransport),
	}
	return r
}

func coalesce(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func defaultConfig() *Config {
	return &Config{
		ActiveContext: "default",
		Contexts:      map[string]Context{},
	}
}
