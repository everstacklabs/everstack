package sandbox

import (
	"os"
	"strings"
)

// ReconcilerEnabled reports whether the async sandbox lifecycle
// reconciler (internal/orchestrator/sandbox) is the lifecycle driver.
// Mirrors cmd/serve's flag read so the manager can gate the legacy
// reaper and delegate lifecycle mutations without importing cmd/serve.
func ReconcilerEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("EVS_SANDBOX_RECONCILER_ENABLED")))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

// DefaultImageBase is the GHCR image prefix for all sandbox images.
// Used as the single source of truth for image references across the codebase.
const DefaultImageBase = "ghcr.io/everstacklabs/sandbox"

// DefaultBaseImage is the minimal foundational sandbox image. It remains
// available for internal helper containers and operators who want a small base.
const DefaultBaseImage = DefaultImageBase + ":base"

// DefaultDevImage is the general-purpose interactive development image. Manual
// sandboxes should feel like Daytona-style dev boxes, so they default to the
// fullstack image instead of the minimal base image.
const DefaultDevImage = DefaultImageBase + ":fullstack"

// DefaultBrowserImage is the default browser sidecar image.
const DefaultBrowserImage = DefaultImageBase + ":browser"

// Per-template images. The template catalog references these so the
// FE and lifecycle layer carry distinct image identities even on
// backends that currently flatten everything to a single rootfs
// (firecracker-agent). When the per-template rootfs pipeline lands
// these become real and the existing wiring just works.
const (
	NodeImage      = DefaultImageBase + ":node"
	DenoImage      = DefaultImageBase + ":deno"
	PythonImage    = DefaultImageBase + ":python"
	FullstackImage = DefaultDevImage
	GoImage        = DefaultImageBase + ":go"
	RustImage      = DefaultImageBase + ":rust"
)

// SandboxConfig is parsed from an agent definition's config JSONB.
// Mirrors the pattern from tools.ParseSpawnConfig.
type SandboxConfig struct {
	Enabled        bool              `json:"enabled"`
	Image          string            `json:"image"`
	CPULimit       float64           `json:"cpu_limit"`
	MemoryMB       int64             `json:"memory_mb"`
	DiskMB         int64             `json:"disk_mb"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	NetworkMode    string            `json:"network_mode"`
	AllowedHosts   []string          `json:"allowed_hosts"`
	EnvVars        map[string]string `json:"env_vars"`
	Tools          []string          `json:"tools"`
	// IdleRetentionSeconds overrides the plan-tier retention when > 0.
	// A value of -1 means no expiration (sandbox lives until manually destroyed).
	// When 0 (default), the manager resolves retention from the tenant's plan tier.
	IdleRetentionSeconds int `json:"idle_retention_seconds"`
	// Name is an optional friendly name for the sandbox instance.
	Name string `json:"name"`

	// WorkDir is where the agent stores its workspace files — repo
	// clones, scratch data, generated artifacts. Surfaced in the
	// composer's "Working Directory" field. Plumbed all the way down
	// to InstanceConfig.WorkDir, which the firecracker shell uses as
	// the initial cwd and which sandbox_file_tools uses for path
	// validation. Empty → backend falls back to /workspace.
	WorkDir string `json:"work_dir,omitempty"`

	// Git import fields
	GitRepoURL        string `json:"git_repo_url"`
	GitBranch         string `json:"git_branch"`
	GitInstallationID int64  `json:"git_installation_id"`

	// SSH toggle
	SSHEnabled bool `json:"ssh_enabled"`

	// KeepWarm keeps the sandbox alive between webhook/cron invocations.
	// When true, the sandbox uses a longer idle timeout (DefaultKeepWarmIdleSecs)
	// and is only reaped when no active triggers remain.
	KeepWarm bool `json:"keep_warm"`

	// Persistent marks this sandbox as a persistent trooper keyed by agent ID.
	// Persistent troopers survive across sessions, sleep on idle, wake on demand,
	// and are gated by plan tier limits. Cloud-only feature.
	Persistent bool `json:"persistent"`

	// LinkedSessionID, when non-empty, attaches the agent to an existing
	// sandbox identified by its session ID. Mutually exclusive with Persistent
	// creating a new sandbox — the agent reuses the linked sandbox instead.
	LinkedSessionID string `json:"linked_session_id"`

	// AgentID is set at runtime for persistent troopers. Serialized to JSON so
	// the Kubernetes pod config annotation preserves it across process restarts.
	AgentID string `json:"agent_id,omitempty"`

	// BrowserSidecar, when set, provisions a browser sidecar container alongside
	// the sandbox. K8s: additional container in the pod. Docker: linked container.
	BrowserSidecar *BrowserSidecarConfig `json:"browser_sidecar,omitempty"`

	// TailscaleAuthKey, when set, causes the sandbox-agent to join the
	// customer's Tailnet at boot via `tailscale up --authkey`. The sandbox
	// appears as a device with a Tailscale IP. Use an ephemeral key so the
	// device auto-expires when the sandbox is destroyed.
	TailscaleAuthKey string `json:"tailscale_auth_key,omitempty"`
	// StorageMounts lists external storage (S3, R2, GCS, Azure) to FUSE-mount at boot.
	// Injected as SANDBOX_MOUNTS_JSON env var for the sandbox-agent.
	StorageMounts []StorageMountConfig `json:"storage_mounts,omitempty"`

	// Labels are arbitrary key-value metadata set at sandbox creation time.
	// Used by agent orchestrators to tag sandboxes by run ID, agent ID, repo,
	// PR number, etc. Filterable via ListSandboxInstances label_filter.
	Labels map[string]string `json:"labels,omitempty"`

	// SnapshotID is the named snapshot this sandbox was created from (if any).
	// Recorded on the sandbox row and included in list responses.
	SnapshotID string `json:"snapshot_id,omitempty"`
	// AutoArchiveAfterDays: days after which a stopped (sleeping) sandbox is
	// archived. 0 = disabled (ArchiveChecker skips it). Default: 7.
	AutoArchiveAfterDays int `json:"auto_archive_after_days,omitempty"`
	// AutoDeleteAfterDays: days after which a stopped or archived sandbox is
	// deleted. -1 = never (default). 0 = delete immediately on stop.
	AutoDeleteAfterDays int `json:"auto_delete_after_days,omitempty"`
	// Daytona-style minute intervals (canonical going forward). Nil
	// pointers mean "not supplied": auto_stop falls back to the plan
	// tier default, archive/delete fall back to the day fields above.
	//   AutoStopMinutes:    0 = disabled
	//   AutoArchiveMinutes: 0 = disabled
	//   AutoDeleteMinutes:  -1 = never, 0 = ephemeral (delete on stop)
	AutoStopMinutes    *int `json:"auto_stop_minutes,omitempty"`
	AutoArchiveMinutes *int `json:"auto_archive_minutes,omitempty"`
	AutoDeleteMinutes  *int `json:"auto_delete_minutes,omitempty"`
	// NetworkBlockAll blocks all outbound egress from the sandbox.
	// Always-allowed: loopback, link-local, DNS, fcagent TAP range.
	// Applied by the sandbox-agent at boot via nftables/iptables.
	NetworkBlockAll bool `json:"network_block_all,omitempty"`
	// NetworkAllowCIDRs lists CIDR blocks to permit when NetworkBlockAll is true.
	// Max 10 entries. Injected as SANDBOX_NETWORK_ALLOW_CIDRS env var.
	NetworkAllowCIDRs []string `json:"network_allow_cidrs,omitempty"`
	// ComputerUse enables Xvfb + XFCE4 desktop at sandbox boot.
	// Injected as SANDBOX_COMPUTER_USE=1 env var for sandbox-agent.
	ComputerUse bool `json:"computer_use,omitempty"`
}

// StorageMountConfig describes an external storage bucket to FUSE-mount inside a sandbox.
type StorageMountConfig struct {
	Type      string `json:"type"`               // "s3" | "r2" | "gcs" | "azure"
	Bucket    string `json:"bucket"`             // bucket/container name
	MountPath string `json:"mount_path"`         // absolute path inside sandbox
	Endpoint  string `json:"endpoint,omitempty"` // custom S3-compatible endpoint
	SubPath   string `json:"subpath,omitempty"`  // subpath within bucket
	ReadOnly  bool   `json:"read_only,omitempty"`
	// Per-mount S3/R2 credentials (optional). Set by the everstack-volume
	// rewrite to a tenant-scoped, bucket-scoped R2 token; applied to the mount
	// subprocess env only. Empty = inherit the agent env (legacy s3/r2 mounts).
	AccessKeyID     string `json:"access_key_id,omitempty"`
	SecretAccessKey string `json:"secret_access_key,omitempty"`
	SessionToken    string `json:"session_token,omitempty"`
}

// BrowserConfig controls browser automation via CDP (Chrome DevTools Protocol).
type BrowserConfig struct {
	Enabled    bool `json:"enabled"`
	Headless   bool `json:"headless"`    // default: true
	CDPPort    int  `json:"cdp_port"`    // default: 9222
	StreamPort int  `json:"stream_port"` // default: 6080 (WebSocket streamer)
}

// ParseBrowserConfig extracts browser automation config from the agent config map.
func ParseBrowserConfig(config map[string]interface{}) BrowserConfig {
	cfg := BrowserConfig{
		Headless:   true,
		CDPPort:    9222,
		StreamPort: 6080,
	}
	if config == nil {
		return cfg
	}
	browserRaw, ok := config["browser"].(map[string]interface{})
	if !ok {
		return cfg
	}
	if enabled, ok := browserRaw["enabled"].(bool); ok {
		cfg.Enabled = enabled
	}
	if headless, ok := browserRaw["headless"].(bool); ok {
		cfg.Headless = headless
	}
	if cdpPort, ok := browserRaw["cdp_port"].(float64); ok && cdpPort > 0 && cdpPort <= 65535 {
		cfg.CDPPort = int(cdpPort)
	}
	if streamPort, ok := browserRaw["stream_port"].(float64); ok && streamPort > 0 && streamPort <= 65535 {
		cfg.StreamPort = int(streamPort)
	}
	return cfg
}

// ToSidecarConfig converts a BrowserConfig into a BrowserSidecarConfig
// suitable for attaching to an InstanceConfig. Returns nil if browser is disabled.
func (bc BrowserConfig) ToSidecarConfig() *BrowserSidecarConfig {
	if !bc.Enabled {
		return nil
	}
	return &BrowserSidecarConfig{
		Image:      DefaultBrowserImage,
		Headless:   bc.Headless,
		CDPPort:    bc.CDPPort,
		StreamPort: bc.StreamPort,
	}
}

// PeerConfig controls cross-agent communication.
type PeerConfig struct {
	// Enabled toggles cross-agent messaging for this agent.
	Enabled bool `json:"enabled"`
	// AllowedPeers is a list of agent names/IDs that can message this agent.
	// Use "*" to allow any agent in the same tenant.
	AllowedPeers []string `json:"allowed_peers"`
}

// ParsePeerConfig extracts peer communication config from the agent config map.
func ParsePeerConfig(config map[string]interface{}) PeerConfig {
	var cfg PeerConfig
	if config == nil {
		return cfg
	}
	peerRaw, ok := config["peer"].(map[string]interface{})
	if !ok {
		return cfg
	}
	if enabled, ok := peerRaw["enabled"].(bool); ok {
		cfg.Enabled = enabled
	}
	if peers, ok := peerRaw["allowed_peers"].([]interface{}); ok {
		for _, p := range peers {
			if s, ok := p.(string); ok {
				cfg.AllowedPeers = append(cfg.AllowedPeers, s)
			}
		}
	}
	return cfg
}

// DefaultWorkDir is where a sandbox's interactive shell, agent file
// tools, and persistent workspace land when the user hasn't picked a
// different path. Used as the single fallback for every provisioning
// path so adding a new backend / template can't accidentally invent
// a divergent default.
const DefaultWorkDir = "/workspace"

// resolveWorkDir returns the workdir to use on an InstanceConfig:
// the configured path if set, the canonical default otherwise. Kept
// in this package (not inline at each call site) so future changes
// to the fallback only have to happen in one place — and so calls in
// `manager.go` read as a clear intent rather than a magic string.
func resolveWorkDir(configured string) string {
	if wd := strings.TrimSpace(configured); wd != "" {
		return wd
	}
	return DefaultWorkDir
}

// DefaultAllowedHosts returns the package-registry hosts that whitelist mode
// falls back to when no explicit allowlist is supplied. Exposed so the
// persistent-agent provisioning path (which bypasses ParseSandboxConfig)
// can apply the same default. Returns a fresh slice each call so callers
// can append without aliasing the package-level value.
func DefaultAllowedHosts() []string {
	return append([]string(nil), defaultAllowedHosts...)
}

// defaultAllowedHosts is retained for older configs that explicitly opt into
// whitelist mode.
var defaultAllowedHosts = []string{
	// npm / yarn
	"registry.npmjs.org",
	"*.npmjs.org",
	"*.yarnpkg.com",
	// pip / PyPI
	"pypi.org",
	"files.pythonhosted.org",
	// cargo / crates.io
	"crates.io",
	"static.crates.io",
	"index.crates.io",
	// Go modules
	"proxy.golang.org",
	"sum.golang.org",
}

// MaxSandboxDiskMB is a sanity ceiling on a sandbox's provisioned root disk
// (1 TiB). It is not a product cap: storage is uncapped and billed per tier
// (free up to IncludedDiskGiB, base rate to DiskTier2ThresholdGiB, +25%
// beyond). The ceiling only guards the host from absurd allocations and a
// typo'd disk_mb from producing a runaway bill. Untyped so it compares
// cleanly against both int64 and float64 disk values.
const MaxSandboxDiskMB = 1048576

// DefaultSandboxConfig returns a SandboxConfig with practical VM defaults.
func DefaultSandboxConfig() SandboxConfig {
	return SandboxConfig{
		Enabled:        false,
		CPULimit:       0.5,
		MemoryMB:       512,
		DiskMB:         20480, // Nano: the smallest fixed managed size; self-hosted callers may override it.
		TimeoutSeconds: 300,
		NetworkMode:    "allow",
	}
}

// ParseSandboxConfig extracts sandbox configuration from the agent config map.
// If a "template" field is present (ID or slug), the template's values are used
// as the base and any explicit fields in the config override them.
func ParseSandboxConfig(config map[string]interface{}) SandboxConfig {
	cfg := DefaultSandboxConfig()
	if config == nil {
		return cfg
	}
	sandboxRaw, ok := config["sandbox"]
	if !ok {
		return cfg
	}
	sandboxMap, ok := sandboxRaw.(map[string]interface{})
	if !ok {
		return cfg
	}

	// Resolve template as base config if specified
	if tplID, ok := sandboxMap["template"].(string); ok && tplID != "" {
		if tpl := GetTemplate(tplID); tpl != nil {
			cfg = TemplateToSandboxConfig(tpl)
		}
	}

	if enabled, ok := sandboxMap["enabled"].(bool); ok {
		cfg.Enabled = enabled
	}
	if image, ok := sandboxMap["image"].(string); ok && image != "" {
		cfg.Image = image
	}
	if cpu, ok := sandboxMap["cpu_limit"].(float64); ok && cpu > 0 && cpu <= 8 {
		cfg.CPULimit = cpu
	}
	if mem, ok := sandboxMap["memory_mb"].(float64); ok && mem >= 64 && mem <= 8192 {
		cfg.MemoryMB = int64(mem)
	}
	if disk, ok := sandboxMap["disk_mb"].(float64); ok && disk >= 64 && disk <= MaxSandboxDiskMB {
		cfg.DiskMB = int64(disk)
	}
	if timeout, ok := sandboxMap["timeout_seconds"].(float64); ok && timeout >= 30 && timeout <= 3600 {
		cfg.TimeoutSeconds = int(timeout)
	}
	if netMode, ok := sandboxMap["network_mode"].(string); ok {
		switch netMode {
		case "deny":
			cfg.NetworkMode = "deny"
			cfg.AllowedHosts = nil // explicit deny clears any default hosts
		case "whitelist":
			cfg.NetworkMode = "whitelist"
		case "allow":
			cfg.NetworkMode = "allow"
			cfg.AllowedHosts = nil // full access, no whitelist needed
		}
	}
	if hosts, ok := sandboxMap["allowed_hosts"].([]interface{}); ok {
		// Explicit allowed_hosts replaces the defaults (not appends)
		cfg.AllowedHosts = nil
		for _, h := range hosts {
			if s, ok := h.(string); ok {
				cfg.AllowedHosts = append(cfg.AllowedHosts, s)
			}
		}
	}

	// If the agent picked whitelist but didn't supply any hosts, fall back
	// to the package-registry defaults instead of "block everything." The
	// admin form has promised "npm, PyPI, cargo, Go are always included by
	// default" — this is what makes that true. Empty-by-explicit-omission
	// is treated as "use defaults"; if a user really wants "whitelist with
	// nothing reachable" they can switch to deny.
	if cfg.NetworkMode == "whitelist" && len(cfg.AllowedHosts) == 0 {
		cfg.AllowedHosts = append([]string(nil), defaultAllowedHosts...)
	}
	if envRaw, ok := sandboxMap["env_vars"].(map[string]interface{}); ok {
		cfg.EnvVars = make(map[string]string, len(envRaw))
		for k, v := range envRaw {
			if s, ok := v.(string); ok {
				cfg.EnvVars[k] = s
			}
		}
	}
	if tools, ok := sandboxMap["tools"].([]interface{}); ok {
		for _, t := range tools {
			if s, ok := t.(string); ok {
				cfg.Tools = append(cfg.Tools, s)
			}
		}
	}
	if idleRetention, ok := sandboxMap["idle_retention_seconds"].(float64); ok {
		cfg.IdleRetentionSeconds = int(idleRetention)
	}
	if name, ok := sandboxMap["name"].(string); ok {
		cfg.Name = name
	}
	// WorkDir resolution order, highest precedence first:
	//   1. sandbox.work_dir — explicit override inside the sandbox section
	//   2. working_directory — top-level agent setting (the composer's
	//      "Working Directory" input writes here)
	// This matches the existing engine.go convention (which already reads
	// the top-level working_directory for path validation) and gives
	// templates / per-agent overrides a clear precedence.
	if wd, ok := sandboxMap["work_dir"].(string); ok && strings.TrimSpace(wd) != "" {
		cfg.WorkDir = strings.TrimSpace(wd)
	} else if wd, ok := config["working_directory"].(string); ok && strings.TrimSpace(wd) != "" {
		cfg.WorkDir = strings.TrimSpace(wd)
	}
	if gitRepoURL, ok := sandboxMap["git_repo_url"].(string); ok {
		cfg.GitRepoURL = gitRepoURL
	}
	if gitBranch, ok := sandboxMap["git_branch"].(string); ok {
		cfg.GitBranch = gitBranch
	}
	if installationID, ok := sandboxMap["git_installation_id"].(float64); ok {
		cfg.GitInstallationID = int64(installationID)
	}
	if sshEnabled, ok := sandboxMap["ssh_enabled"].(bool); ok {
		cfg.SSHEnabled = sshEnabled
	}
	if persistent, ok := sandboxMap["persistent"].(bool); ok {
		cfg.Persistent = persistent
	}
	if linkedSessionID, ok := sandboxMap["linked_session_id"].(string); ok {
		cfg.LinkedSessionID = linkedSessionID
	}

	return cfg
}

// GlobalSandboxConfig is set at gateway startup level.
type GlobalSandboxConfig struct {
	Enabled                  bool     `json:"enabled"`
	Backend                  string   `json:"backend"`
	MaxSandboxes             int      `json:"max_sandboxes"`
	AllowedImages            []string `json:"allowed_images"`
	DefaultImage             string   `json:"default_image"`
	MaxCPU                   float64  `json:"max_cpu"`
	MaxMemoryMB              int64    `json:"max_memory_mb"`
	MaxDiskMB                int64    `json:"max_disk_mb"`
	MaxTimeoutSecs           int      `json:"max_timeout_seconds"`
	DefaultIdleRetentionSecs int      `json:"default_idle_retention_seconds"`
	MaxIdleRetentionSecs     int      `json:"max_idle_retention_seconds"`
	DNSServers               []string `json:"dns_servers"`

	// DataDir is the host-side directory for sandbox data (repo clones, snapshots).
	// Defaults to /var/lib/everstack/sandboxes/
	DataDir string `json:"data_dir"`

	// MaxConcurrentCreates limits the number of sandbox creations running in
	// parallel. Prevents system overload from burst traffic (e.g. 50 webhooks
	// firing simultaneously). Default: 5.
	MaxConcurrentCreates int `json:"max_concurrent_creates"`

	// DefaultKeepWarmIdleSecs is the idle timeout for keep-warm sandboxes
	// (those with active webhook/cron triggers). After this period of inactivity,
	// the sandbox is eligible for reaping even if triggers exist. Default: 300 (5 min).
	DefaultKeepWarmIdleSecs int `json:"default_keep_warm_idle_seconds"`

	// DefaultIdleSleepSecs is the idle-to-sleep timeout used when a sandbox's
	// IdleRetentionSecs is 0 ("never auto-terminate"). Without this, "Never
	// expire" sandboxes ran forever on compute. The sleep transition snapshots
	// the workspace and stops the container; the snapshot stays revivable per
	// the tenant's stop-retention policy, so "Never" still means data lives
	// indefinitely. Default: 1800 (30 min). Set to 0 to opt out fully (back
	// to the old never-sleep behavior).
	DefaultIdleSleepSecs int `json:"default_idle_sleep_seconds"`

	// Pricing config controls compute cost estimation/metering.
	Pricing SandboxPricingConfig `json:"pricing"`

	// WarmImages is the list of images the gateway pre-pulls at startup so
	// the first sandbox create does not pay a multi-second image-pull tax.
	// Empty list disables warming. Pulls run in the background; failures are
	// logged and ignored. Only honored by backends implementing ImageWarmer
	// (currently Docker).
	WarmImages []string `json:"warm_images"`
}

// SandboxPricingConfig defines sandbox compute pricing rates.
type SandboxPricingConfig struct {
	Enabled  bool   `json:"enabled"`
	Currency string `json:"currency"`

	CPUPerHourUSD         float64            `json:"cpu_per_hour_usd"`
	MemoryGBPerHourUSD    float64            `json:"memory_gb_per_hour_usd"`
	DiskGBPerHourUSD      float64            `json:"disk_gb_per_hour_usd"`
	PlatformFeePerHourUSD float64            `json:"platform_fee_per_hour_usd"`
	TierMultipliers       map[string]float64 `json:"tier_multipliers"`

	// Storage allowance + overage tiering. The first IncludedDiskGiB of a
	// sandbox's root disk is included in its fixed machine rate. Disk above the
	// allowance and up to DiskTier2ThresholdGiB is billed at DiskGBPerHourUSD;
	// disk beyond DiskTier2ThresholdGiB is billed at the rate times
	// DiskTier2Multiplier (marginal tiering — only the portion in each band
	// is charged at that band's rate).
	IncludedDiskGiB       float64 `json:"included_disk_gib"`
	DiskTier2ThresholdGiB float64 `json:"disk_tier2_threshold_gib"`
	DiskTier2Multiplier   float64 `json:"disk_tier2_multiplier"`
}

// DefaultGlobalSandboxConfig returns global config with safe defaults.
func DefaultGlobalSandboxConfig() GlobalSandboxConfig {
	return GlobalSandboxConfig{
		Enabled:      false,
		Backend:      "docker",
		MaxSandboxes: 500,
		DefaultImage: DefaultDevImage,
		MaxCPU:       4.0,
		MaxMemoryMB:  4096,
		// Storage is uncapped as a product matter (billed per tier); the
		// sanity ceiling only protects the host. Operators/tenants can set a
		// smaller per-deployment cap via runtime_config.
		MaxDiskMB:                MaxSandboxDiskMB,
		MaxTimeoutSecs:           3600,
		DefaultIdleRetentionSecs: 1 * 24 * 60 * 60,   // 1 day
		MaxIdleRetentionSecs:     365 * 24 * 60 * 60, // 1 year
		// Empty means "inherit the backend/runtime resolver." For
		// firecracker-agent this lets the VM use the agent pod's
		// Kubernetes DNS path instead of hard-coding public resolvers
		// that many clusters block.
		DNSServers:              nil,
		MaxConcurrentCreates:    5,
		DefaultKeepWarmIdleSecs: 300,
		DefaultIdleSleepSecs:    30 * 60, // 30 min — even "Never expire" sandboxes sleep when idle
		WarmImages:              []string{DefaultDevImage},
		Pricing: SandboxPricingConfig{
			Enabled:  true,
			Currency: "USD",
			// Compute rates (vCPU/memory) are Daytona-aligned; storage keeps
			// the Blaxel-aligned volume-runtime rate.
			//   vCPU:    $0.0504 / vCPU-hour
			//   memory:  $0.0162 / GiB-hour
			//   storage: $0.00000004629 / GiB-second => $0.000166644 / GiB-hour
			CPUPerHourUSD:         0.0504,
			MemoryGBPerHourUSD:    0.0162,
			DiskGBPerHourUSD:      0.000166644,
			PlatformFeePerHourUSD: 0.0,
			// Each fixed machine rate includes 20 GiB of dedicated root
			// storage. Storage from 20-50 GiB is billed at the base rate;
			// beyond 50 GiB the marginal rate rises 25%.
			IncludedDiskGiB:       20.0,
			DiskTier2ThresholdGiB: 50.0,
			DiskTier2Multiplier:   1.25,
			TierMultipliers: map[string]float64{
				"free":       1.0,
				"basic":      1.0,
				"pro":        0.93,
				"enterprise": 0.88,
			},
		},
	}
}
