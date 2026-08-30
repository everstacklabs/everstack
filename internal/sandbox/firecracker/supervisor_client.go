package firecracker

// HTTP client for the host-side fc-supervisor daemon.
//
// fc-supervisor is the binary defined in services/fc-supervisor/. It
// listens on a unix socket on the host and owns the lifecycle of
// every Everstack firecracker VM via systemd transient scopes. By
// delegating spawn/stop to it, fcagent stops being the cgroup
// parent of its firecracker children — pod replacement no longer
// destroys VMs.
//
// Design rationale + failure modes in docs/design/fc-supervisor.md.
//
// This file is the CLIENT side. It's only used when the
// FC_SUPERVISOR_SOCKET env var (read at backend init) points at a
// reachable socket. When unset or unreachable, fcagent falls back
// to the direct exec.Command path (current behavior). The fallback
// keeps deployments that haven't installed the supervisor yet
// fully functional.
//
// Wire-format structs duplicated from services/fc-supervisor/
// protocol.go on purpose — both ends pin the contract and we don't
// want to cross-import the supervisor binary's package from
// production code.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// SupervisorClient talks to fc-supervisor over its unix socket.
// Stateless and safe for concurrent use; the underlying http.Client
// pools and reuses connections to the unix socket.
type SupervisorClient struct {
	socketPath string
	http       *http.Client
}

// NewSupervisorClient builds a client targeted at the given unix
// socket path. Does NOT verify the socket is reachable — callers
// should call Health() to confirm before relying on it.
func NewSupervisorClient(socketPath string) *SupervisorClient {
	return &SupervisorClient{
		socketPath: socketPath,
		http: &http.Client{
			// Per-request timeout is a backstop; callers always
			// pass a context with their own deadline. Set this
			// high enough that the supervisor's own spawn budget
			// (10s + headroom) fits comfortably inside.
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socketPath)
				},
				// Pool a small number of idle conns to the
				// supervisor. We don't expect high request
				// concurrency from one fcagent — most operations
				// are 1 spawn / 1 stop per sandbox event.
				MaxIdleConns:        4,
				IdleConnTimeout:     60 * time.Second,
				DisableCompression:  true,
			},
		},
	}
}

// supervisorSpawnRequest mirrors services/fc-supervisor/protocol.go
// SpawnRequest. Keeping the type local so this package doesn't
// import the supervisor binary.
type supervisorSpawnRequest struct {
	SandboxID  string `json:"sandbox_id"`
	BinaryPath string `json:"binary_path"`
	APISocket  string `json:"api_sock"`
	WorkDir    string `json:"work_dir"`
	MemoryMB   int64  `json:"mem_mb"`
	VCPUs      int    `json:"vcpus"`
}

// SupervisorSpawnResponse mirrors the supervisor's wire-format
// reply. Exposed so vm.go can persist ScopeName + MainPID onto the
// MicroVM struct.
type SupervisorSpawnResponse struct {
	ScopeName string `json:"scope_name"`
	MainPID   int    `json:"main_pid"`
}

// supervisorErrorResponse is the body the supervisor sends on
// non-2xx responses. Used to surface the supervisor's error text
// into the caller's error log.
type supervisorErrorResponse struct {
	Error string `json:"error"`
}

// ErrSupervisorUnreachable is returned when the supervisor socket
// can't be dialed. The caller treats this as "fall back to direct
// exec" rather than a hard failure — same VM behavior as the pre-
// supervisor world, just without the cgroup-isolation property.
var ErrSupervisorUnreachable = errors.New("fc-supervisor socket unreachable")

// Health pings the supervisor's /healthz. Used at backend startup
// to decide whether the supervisor is wired and ready, before
// fcagent commits to routing spawns through it.
func (c *SupervisorClient) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://supervisor/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return wrapDialErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("supervisor healthz: unexpected status %d", resp.StatusCode)
	}
	return nil
}

// Spawn asks the supervisor to launch a firecracker VM in a fresh
// systemd transient scope. Returns the scope name + main PID on
// success.
func (c *SupervisorClient) Spawn(ctx context.Context, opts LaunchOptions) (*SupervisorSpawnResponse, error) {
	body, err := json.Marshal(supervisorSpawnRequest{
		SandboxID:  opts.SandboxID,
		BinaryPath: opts.BinaryPath,
		APISocket:  opts.APISocket,
		WorkDir:    opts.WorkDir,
		MemoryMB:   opts.MemoryMB,
		VCPUs:      opts.VCPUs,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal spawn request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://supervisor/vms/spawn", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, wrapDialErr(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var er supervisorErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&er)
		return nil, fmt.Errorf("supervisor spawn %s: status %d: %s", opts.SandboxID, resp.StatusCode, er.Error)
	}

	var out SupervisorSpawnResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode spawn response: %w", err)
	}
	return &out, nil
}

// Stop asks the supervisor to stop the named sandbox's scope.
// Idempotent: stopping an unknown sandbox returns nil.
func (c *SupervisorClient) Stop(ctx context.Context, sandboxID string) error {
	url := fmt.Sprintf("http://supervisor/vms/%s/stop", sandboxID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return wrapDialErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	var er supervisorErrorResponse
	_ = json.NewDecoder(resp.Body).Decode(&er)
	return fmt.Errorf("supervisor stop %s: status %d: %s", sandboxID, resp.StatusCode, er.Error)
}

// LaunchOptions is the input the supervisor needs to spawn a VM.
// Mirrors the SpawnRequest wire-format struct in the supervisor
// binary. Exposed at package level so vm.go can build it once and
// reuse for both the supervisor path and the direct-exec fallback.
type LaunchOptions struct {
	SandboxID  string
	BinaryPath string
	APISocket  string
	WorkDir    string
	MemoryMB   int64
	VCPUs      int
}

// wrapDialErr converts a low-level dial / connection error into
// ErrSupervisorUnreachable so callers can dispatch on the typed
// sentinel rather than scraping error text.
func wrapDialErr(err error) error {
	if err == nil {
		return nil
	}
	// net.OpError wraps a *net.OpError or *os.PathError depending
	// on the failure mode; both surface as Op=="dial" / Op=="connect".
	// Use errors.Is against syscall errors for the canonical cases.
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return fmt.Errorf("%w: %v", ErrSupervisorUnreachable, err)
	}
	return err
}
