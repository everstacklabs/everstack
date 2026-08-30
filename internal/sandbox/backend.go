package sandbox

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox/snapshot"
)

// ErrSandboxNotRunning is returned when the sandbox container/VM has died or
// been removed and can no longer execute commands. Callers should treat this
// as a recoverable error — the sandbox can be recreated and the operation retried.
var ErrSandboxNotRunning = errors.New("sandbox is not running")

// ErrSandboxRouteMissing is returned by remote/routed backends when the
// process lost the in-memory route for a sandbox that may still exist at the
// backend. Callers should try durable route rehydration before treating the
// sandbox as gone.
var ErrSandboxRouteMissing = errors.New("sandbox route is missing")

// Status represents the lifecycle state of a sandbox instance.
type Status string

const (
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusStopped Status = "stopped"
	StatusFailed  Status = "failed"
)

// NetworkMode controls network access from within the sandbox.
type NetworkMode string

const (
	NetworkDeny      NetworkMode = "deny"
	NetworkWhitelist NetworkMode = "whitelist"
	NetworkAllow     NetworkMode = "allow"
)

// Backend defines the stateful sandbox interface.
// Unlike isolation.Backend (fire-and-forget execution), sandbox.Backend
// manages long-lived environments that persist across multiple operations
// within a session.
type Backend interface {
	// Name returns the backend identifier (e.g. "docker", "firecracker").
	Name() string
	// Create provisions a new long-lived sandbox. The container/VM stays running.
	Create(ctx context.Context, id string, config InstanceConfig) (*Instance, error)
	// Exec runs a command inside a running sandbox.
	Exec(ctx context.Context, id string, cmd ExecRequest) (*ExecResult, error)
	// WriteFile writes content to a path inside the sandbox.
	WriteFile(ctx context.Context, id string, path string, content []byte) error
	// ReadFile reads content from a path inside the sandbox.
	ReadFile(ctx context.Context, id string, path string) ([]byte, error)
	// ListFiles lists directory contents inside the sandbox.
	ListFiles(ctx context.Context, id string, path string) ([]FileInfo, error)
	// Destroy tears down the sandbox and releases all resources.
	Destroy(ctx context.Context, id string) error
	// Status returns the current sandbox state.
	Status(ctx context.Context, id string) (*Instance, error)
	// DescribePending returns a backend-specific diagnostic string for a sandbox
	// that exists but is still pending startup. Returns empty when unavailable.
	DescribePending(ctx context.Context, id string) string
	// Healthy checks if the backend is operational.
	Healthy(ctx context.Context) error
	// Logs returns a stream of container/VM logs.
	Logs(ctx context.Context, id string, opts LogsOptions) (io.ReadCloser, error)
	// Stats returns a one-shot container/VM resource usage snapshot.
	Stats(ctx context.Context, id string) (*ContainerStats, error)
	// Shell opens an interactive shell session inside the sandbox.
	Shell(ctx context.Context, id string, cmd []string) (*ShellSession, error)
	// List returns all sandbox instances known to the backend (for discovery on restart).
	List(ctx context.Context) ([]*Instance, error)
}

// ImageWarmer is optionally implemented by backends that benefit from
// pre-pulling images at gateway startup. The Docker backend implements it
// because cold-pulling a multi-hundred-MB image on the first sandbox create
// can stall the request for tens of seconds. Kubernetes (kubelet handles
// pulls per node) and Firecracker (uses pre-baked rootfs) do not implement
// this interface; the manager skips warming for them.
type ImageWarmer interface {
	EnsureImage(ctx context.Context, image string) error
}

// RouteSeeder is optionally implemented by backends that need a durable
// sandboxID -> target mapping to reach a remote worker. The firecracker-agent
// backend uses this to recover sticky routes from sandbox_instances.agent_target
// after a gateway restart.
type RouteSeeder interface {
	SeedRoute(sandboxID, target string)
	SeedRoutes(routes map[string]string)
}

// InstanceConfig defines the resource limits and settings for a sandbox.
type InstanceConfig struct {
	Image          string            `json:"image"`
	CPULimit       float64           `json:"cpu_limit"`
	MemoryMB       int64             `json:"memory_mb"`
	DiskMB         int64             `json:"disk_mb"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	NetworkMode    NetworkMode       `json:"network_mode"`
	AllowedHosts   []string          `json:"allowed_hosts,omitempty"`
	EnvVars        map[string]string `json:"env_vars,omitempty"`
	WorkDir        string            `json:"work_dir"`
	TenantID       string            `json:"tenant_id"`
	SessionID      string            `json:"session_id"`
	Name           string            `json:"name"`
	DNSServers     []string          `json:"dns_servers,omitempty"`

	// Git import: host-side path to cloned repo (read-only bind mount at /repo)
	GitRepoURL        string `json:"git_repo_url,omitempty"`
	GitBranch         string `json:"git_branch,omitempty"`
	GitCommitSHA      string `json:"git_commit_sha,omitempty"`
	GitInstallationID int64  `json:"git_installation_id,omitempty"`
	RepoHostPath      string `json:"repo_host_path,omitempty"`

	// SSH toggle
	SSHEnabled bool `json:"ssh_enabled,omitempty"`

	// Browser sidecar — when set, the backend provisions a browser container
	// (K8s: sidecar in the pod, Docker: linked container) running Chromium
	// with CDP exposed. The agent connects via localhost:<CDPPort>.
	BrowserSidecar *BrowserSidecarConfig `json:"browser_sidecar,omitempty"`

	// Persistent trooper fields
	AgentID string `json:"agent_id,omitempty"`
}

// BrowserSidecarConfig defines the browser sidecar container settings.
type BrowserSidecarConfig struct {
	Image      string `json:"image"`       // default: DefaultBrowserImage
	Headless   bool   `json:"headless"`    // default: true
	CDPPort    int    `json:"cdp_port"`    // default: 9222
	StreamPort int    `json:"stream_port"` // default: 6080 (WebSocket streamer)
}

// Instance represents a running sandbox environment.
type Instance struct {
	ID          string         `json:"id"`
	ContainerID string         `json:"container_id"`
	Status      Status         `json:"status"`
	Config      InstanceConfig `json:"config"`
	CreatedAt   time.Time      `json:"created_at"`
	// BillingStartedAt is the start of the currently-open compute billing
	// window. CreatedAt is the durable sandbox identity timestamp and must not
	// be reused for metering because a sandbox can sleep and later revive with
	// the same ID. Zero means no compute window is open.
	BillingStartedAt time.Time `json:"billing_started_at,omitempty"`
	// BillingEndedAt pins the observed time compute disappeared while the
	// immutable ledger close is pending. It prevents retry latency from being
	// charged to the customer. It is cleared with BillingStartedAt after the
	// ledger transaction commits.
	BillingEndedAt    time.Time `json:"billing_ended_at,omitempty"`
	ExpiresAt         time.Time `json:"expires_at"`
	LastUsedAt        time.Time `json:"last_used_at"`
	IdleRetentionSecs int       `json:"idle_retention_secs"`
	Backend           string    `json:"backend"`
	InstanceID        string    `json:"instance_id,omitempty"`
	// AgentTarget is the host:port of the specific firecracker-agent
	// pod that owns this sandbox. Populated by the fcagent backend's
	// Create after the load balancer picks a target; left empty by
	// other backends (docker, kubernetes, firecracker — they don't
	// have a per-instance agent address). Persisted to the
	// sandbox_instances row so post-pod-restart routing can resolve
	// without consulting the in-memory route table.
	AgentTarget string `json:"agent_target,omitempty"`
	Name        string `json:"name"`

	// AgentHealthy reflects the most recent in-guest /health probe result
	// from the Firecracker backend's HealthMonitor (Phase 1). true means
	// the agent answered 204 within timeout on its last tick (every 20s).
	// false means it has stopped responding — the VM is up but its
	// userland is wedged or the network path is broken. Used by the admin
	// UI to render a per-sandbox health dot. Non-Firecracker backends
	// always set true; the field carries the strongest signal we have
	// across heterogeneous deployments.
	AgentHealthy bool `json:"agent_healthy"`

	// Lifecycle state (Phase 3)
	LifecycleState     string    `json:"lifecycle_state"`
	TrooperSnapshotRef string    `json:"trooper_snapshot_ref,omitempty"`
	RevivableUntil     time.Time `json:"revivable_until,omitempty"`
	StoppedAt          time.Time `json:"stopped_at,omitempty"`

	// Git source info
	GitRepoURL   string `json:"git_repo_url,omitempty"`
	GitBranch    string `json:"git_branch,omitempty"`
	GitCommitSHA string `json:"git_commit_sha,omitempty"`

	// KeepWarm indicates this sandbox should use a longer idle timeout and
	// survive between webhook/cron invocations.
	KeepWarm bool `json:"keep_warm"`

	// Persistent trooper fields
	Persistent bool   `json:"persistent"`
	AgentID    string `json:"agent_id,omitempty"`

	// ShortCode is the public bitly-style identifier used as the SSH
	// username and the preview URL subdomain on *.evs.run. Stable for
	// the sandbox's lifetime. Empty for legacy rows that predate the
	// backfill.
	ShortCode string `json:"short_code,omitempty"`

	// LastNetworkRxBytes / LastNetworkTxBytes hold the most recent
	// cumulative network counters sampled from the backend. Captured just
	// before teardown (the VM's counters are gone once it's destroyed) so
	// usage metering can persist the sandbox's lifetime data transfer.
	LastNetworkRxBytes int64 `json:"last_network_rx_bytes,omitempty"`
	LastNetworkTxBytes int64 `json:"last_network_tx_bytes,omitempty"`
}

// ExecRequest defines a command to run inside a sandbox.
type ExecRequest struct {
	Command []string          `json:"command"`
	WorkDir string            `json:"work_dir,omitempty"`
	Timeout time.Duration     `json:"timeout"`
	Env     map[string]string `json:"env,omitempty"`
	// SilentLog suppresses teeing this exec's command and stdout/stderr
	// into the per-sandbox log buffer. Set it on internal introspection
	// and plumbing execs (process-session management, file browser, code
	// search, LSP) so the Logs tab reflects only real user/agent activity
	// rather than gateway bookkeeping. Defaults false: normal execs log.
	SilentLog bool `json:"-"`
}

// ExecResult contains the output of a sandbox command execution.
type ExecResult struct {
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	DurationMs int64  `json:"duration_ms"`
	TimedOut   bool   `json:"timed_out"`
}

// LogsOptions controls log retrieval behavior.
type LogsOptions struct {
	Follow     bool
	Tail       int
	Since      time.Time
	Timestamps bool
}

// ContainerStats holds a point-in-time resource usage snapshot.
type ContainerStats struct {
	CPUPercent     float64   `json:"cpu_percent"`
	MemoryUsage    int64     `json:"memory_usage"`
	MemoryLimit    int64     `json:"memory_limit"`
	MemoryPercent  float64   `json:"memory_percent"`
	NetworkRxBytes int64     `json:"network_rx_bytes"`
	NetworkTxBytes int64     `json:"network_tx_bytes"`
	BlockRead      int64     `json:"block_read"`
	BlockWrite     int64     `json:"block_write"`
	PIDs           int       `json:"pids"`
	Timestamp      time.Time `json:"timestamp"`
}

// ShellSession represents an interactive shell attached to a sandbox.
type ShellSession struct {
	Conn   io.ReadWriteCloser
	Resize func(rows, cols uint16) error
	// ShellSessionID is the backend-assigned persistent shell session
	// identifier. The firecracker backend returns this so callers can
	// reattach to the same tmux session across reconnects. Backends
	// that don't support persistence (Docker, Kubernetes today) leave
	// it empty.
	ShellSessionID string
	// Reattached is true when this ShellSession resumed a pre-existing
	// persistent session rather than creating a new one. Informational
	// — used by the gateway to set a "reconnected" banner on the
	// client UI.
	Reattached bool
	// Transport names the host↔guest channel that carried this shell —
	// "vsock" (legacy) or "ws" (Phase 5a/b HTTP control plane). Set by
	// the Firecracker backend based on FCAGENT_SHELL_TRANSPORT. Empty
	// for non-Firecracker backends. Surfaced in the admin UI so
	// operators rolling the new transport can see which path each
	// session landed on without grepping logs.
	Transport string
}

// PersistentShellBackend is implemented by backends that support
// reattachment to a long-running shell session inside the sandbox.
// The manager checks for this interface and routes session-aware
// Shell calls to it when present; backends without persistence keep
// the old "new shell every time" behavior unchanged.
type PersistentShellBackend interface {
	// ShellWithSession opens an interactive shell, optionally
	// reattaching to the given shell session ID. Empty shellSessionID
	// creates a fresh session and the assigned ID comes back on
	// ShellSession.ShellSessionID. A non-empty ID that doesn't exist
	// is a hard error — backends MUST NOT silently create a new
	// session under the caller's nose, because callers will think
	// they reattached when they didn't.
	ShellWithSession(ctx context.Context, id, shellSessionID string, cmd []string) (*ShellSession, error)
	// ListShellSessions enumerates persistent shell sessions alive
	// inside the sandbox. Used by the admin UI's session listing and
	// by background cleanup of idle sessions.
	ListShellSessions(ctx context.Context, id string) ([]ShellSessionInfo, error)
	// KillShellSession terminates a specific persistent session.
	// Idempotent — killing a missing session is not an error.
	KillShellSession(ctx context.Context, id, shellSessionID string) error
}

// ShellSessionInfo describes one persistent shell session as
// reported by a PersistentShellBackend.
type ShellSessionInfo struct {
	ID              string
	AttachedClients int
	CreatedUnix     int64
	// LastActivityUnix is when the guest's tmux last saw input or
	// output in this session. Used by the idle-session reaper. Zero
	// when the guest didn't supply a value — treat as "unknown, do
	// not act on this session" rather than as "ancient."
	LastActivityUnix int64
	// IdleSeconds is how long the session has been idle, computed
	// against the GUEST's clock (not the host's). Backends that
	// support persistent sessions populate this; if the guest didn't
	// report last-activity or now, this stays at -1 to signal
	// "unknown."
	IdleSeconds int64
}

// FileInfo describes a file or directory inside a sandbox.
type FileInfo struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"is_dir"`
}

// FileMetadata describes detailed file metadata inside a sandbox.
type FileMetadata struct {
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	IsDir      bool      `json:"is_dir"`
	ModifiedAt time.Time `json:"modified_at"`
	CreatedAt  time.Time `json:"created_at"`
	Owner      string    `json:"owner"`
	Group      string    `json:"group"`
	Mode       uint32    `json:"mode"`
}

// ListeningPort describes a port detected as listening inside a sandbox.
type ListeningPort struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol"` // "tcp" or "tcp6"
	Address  string `json:"address"`  // "0.0.0.0", "127.0.0.1", "::"
	PID      int    `json:"pid"`
	Process  string `json:"process"`
}

// PortDetector is an optional interface that backends can implement to
// auto-detect listening ports inside a sandbox container.
type PortDetector interface {
	DetectListeningPorts(ctx context.Context, id string) ([]ListeningPort, error)
}

// PortExposer is an optional interface that backends can implement to support
// exposing sandbox ports to external traffic.
type PortExposer interface {
	// ExposePort makes a container/pod port reachable from the host.
	// Returns the host port that was bound.
	ExposePort(ctx context.Context, id string, port int, protocol string) (hostPort int, err error)
	// UnexposePort closes an exposed port mapping.
	UnexposePort(ctx context.Context, id string, port int) error
}

// Snapshotter is an optional interface that backends can implement to support
// snapshotting a sandbox's working directory to a tar.gz file on the host.
// Used by StopSandbox to persist /workspace before destroying the container.
// When not implemented, the manager falls back to `docker cp`.
type Snapshotter interface {
	// Snapshot archives srcPath inside the sandbox to a local destPath (tar.gz).
	Snapshot(ctx context.Context, id string, srcPath string, destPath string) error
	// Restore extracts a tar.gz snapshot into destPath inside the sandbox.
	Restore(ctx context.Context, id string, srcPath string, destPath string) error
}

// VMSnapshotter is an optional interface implemented by backends that can
// produce a full-VM snapshot (memory + state + rootfs) to an object-storage
// snapshot.Store. Distinct from Snapshotter, which is only the /workspace
// workspace tarball. Firecracker implements this; docker / k8s / fcagent-
// gateway-side do not (the latter would proxy through to its remote agent).
//
// Implementations are responsible for the per-backend dance of pausing,
// capturing, uploading, and resuming. The manager simply dispatches.
type VMSnapshotter interface {
	SaveVMSnapshot(ctx context.Context, sandboxID, tenantID, agentID string, store snapshot.Store, trigger string) (*snapshot.Manifest, error)
}

// VMRestorer is the inverse of VMSnapshotter: implemented by backends
// that can rehydrate a microVM on this host from an object-storage
// snapshot. The returned Instance is fully booted (memory loaded,
// network reattached, process running) and ready to register in the
// manager's in-memory maps.
//
// Returns an Instance with Status = StatusRunning on success. Errors
// from a missing snapshot bubble up as snapshot.ErrSnapshotMissing
// so callers can distinguish "no work to do" from real failures.
type VMRestorer interface {
	RestoreVMSnapshot(ctx context.Context, sandboxID, tenantID, agentID string, store snapshot.Store) (*Instance, error)
}

// BackendTargeter is an optional interface that backends can implement to
// provide custom backend target addresses for the reverse proxy. When not
// implemented, the manager falls back to "localhost:{hostPort}".
type BackendTargeter interface {
	// BackendTarget returns the host:port address the proxy should dial to
	// reach the given sandbox port.
	BackendTarget(ctx context.Context, id string, port int) (string, error)
}

// PortMapping represents an active port exposure for a sandbox.
type PortMapping struct {
	ID            int64      `json:"id" db:"id"`
	SandboxID     string     `json:"sandbox_id" db:"sandbox_id"`
	SessionID     string     `json:"session_id" db:"session_id"`
	TenantID      string     `json:"tenant_id" db:"tenant_id"`
	Port          int        `json:"port" db:"port"`
	Protocol      string     `json:"protocol" db:"protocol"`
	Subdomain     string     `json:"subdomain" db:"subdomain"`
	HostPort      int        `json:"host_port" db:"host_port"`
	BackendTarget string     `json:"backend_target" db:"backend_target"`
	Status        string     `json:"status" db:"status"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	ClosedAt      *time.Time `json:"closed_at,omitempty" db:"closed_at"`
}
