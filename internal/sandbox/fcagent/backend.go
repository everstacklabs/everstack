// Package fcagent implements a sandbox.Backend that proxies calls to remote
// Firecracker agent pods over gRPC. Each agent pod runs a FirecrackerBackend
// on a KVM-enabled host; the gateway selects an agent and dispatches sandbox
// operations to it.
package fcagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
	fcpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/firecracker/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
)

// fcagentServiceConfig drives client-side gRPC behavior for calls to
// fcagent. Two pieces matter for self-healing under a rolling restart
// of the firecracker-agent DaemonSet:
//
//  1. retryPolicy: any UNAVAILABLE on an idempotent unary RPC is
//     auto-retried by the client transparently, up to 5 attempts with
//     exponential backoff (100ms → 2s). This is how a 200-800ms window
//     during pod re-bind stops surfacing as a user-visible 502.
//
//  2. healthCheckConfig: drives gRPC's built-in client-side health
//     check so the subchannel is marked unhealthy as soon as fcagent's
//     grpc.health.v1 server reports NOT_SERVING. fcagent's main.go
//     flips that bit BEFORE GracefulStop() so we route around the
//     dying pod within milliseconds — without waiting for a TCP-level
//     failure or the discovery refresh tick.
//
// Streaming RPCs (Shell, Logs) are deliberately NOT in the retry list.
// They reattach at the application layer via persistent session ids,
// and a silent retry mid-stream would clobber the user's terminal
// state. The application-layer reattach is what makes streams resilient.
const fcagentServiceConfig = `{
  "healthCheckConfig": { "serviceName": "" },
  "methodConfig": [{
    "name": [
      {"service": "everstack.firecracker.v1.FirecrackerAgent", "method": "Status"},
      {"service": "everstack.firecracker.v1.FirecrackerAgent", "method": "List"},
      {"service": "everstack.firecracker.v1.FirecrackerAgent", "method": "ListShellSessions"},
      {"service": "everstack.firecracker.v1.FirecrackerAgent", "method": "KillShellSession"},
      {"service": "everstack.firecracker.v1.FirecrackerAgent", "method": "NodeHealth"},
      {"service": "everstack.firecracker.v1.FirecrackerAgent", "method": "ExposePort"},
      {"service": "everstack.firecracker.v1.FirecrackerAgent", "method": "UnexposePort"}
    ],
    "retryPolicy": {
      "maxAttempts": 5,
      "initialBackoff": "0.1s",
      "maxBackoff": "2s",
      "backoffMultiplier": 2.0,
      "retryableStatusCodes": ["UNAVAILABLE"]
    }
  }]
}`

// Config controls discovery and connection to remote Firecracker agents.
type Config struct {
	// Service is the K8s headless service name (e.g.
	// "firecracker-agent.everstack.svc.cluster.local") used for DNS discovery,
	// or a comma-separated static list of host:port targets.
	Service string
	// Port is the gRPC port the agents listen on (default 9090).
	Port int
	// RefreshInterval is how often to re-resolve DNS (default 30s).
	RefreshInterval time.Duration
	// DialTimeout is the timeout per dial attempt (default 5s).
	DialTimeout time.Duration

	// TLS enables mTLS between the gateway and agents. When all three paths
	// are empty, plain (insecure) gRPC is used.
	TLS TLSConfig
}

// TLSConfig holds paths to client cert/key and server CA for mTLS.
type TLSConfig struct {
	// ClientCert is the PEM-encoded client certificate presented to agents.
	ClientCert string
	// ClientKey is the PEM-encoded client private key.
	ClientKey string
	// ServerCA is the PEM bundle used to verify agent server certificates.
	ServerCA string
	// ServerName is the SNI / hostname to verify. Required when the target
	// addresses are IPs (e.g. resolved K8s pod IPs) rather than hostnames.
	ServerName string
}

// FCAgentBackend is a gRPC proxy backend for Firecracker agents.
type FCAgentBackend struct {
	cfg         Config
	discovery   *Discovery
	lb          *LoadBalancer
	healthCache *HealthCache

	// Route table: sandbox id -> agent target address.
	// We sticky-route each sandbox to the agent that created it.
	mu     sync.RWMutex
	routes map[string]string

	// routeUpdater is the optional hook that persists the current
	// route target back to sandbox_instances.agent_target. Wired by
	// the manager at startup via SetRouteUpdater. See the
	// RouteUpdater doc and setRoute's call site for the failure
	// mode it closes (stale agent_target after pod replacement).
	routeUpdater RouteUpdater

	portMu       sync.Mutex
	portMappings map[string]map[int]*remotePortMapping
}

type remotePortMapping struct {
	Target   string
	HostPort int
	Protocol string
}

// New constructs a backend and begins discovery.
func New(cfg Config) (*FCAgentBackend, error) {
	if cfg.Service == "" {
		return nil, errors.New("fcagent: Service is required")
	}
	if cfg.Port == 0 {
		cfg.Port = 9090
	}
	if cfg.RefreshInterval == 0 {
		cfg.RefreshInterval = 30 * time.Second
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 5 * time.Second
	}

	dialOpts, err := buildDialOptions(cfg.TLS)
	if err != nil {
		return nil, fmt.Errorf("fcagent: tls: %w", err)
	}

	disc, err := NewDiscovery(cfg.Service, cfg.Port, cfg.RefreshInterval, cfg.DialTimeout, dialOpts)
	if err != nil {
		return nil, fmt.Errorf("fcagent: discovery init: %w", err)
	}

	// Pressure-aware placement: poll each agent's NodeHealth every
	// 10s, cache the result, and let the LoadBalancer skip degraded
	// hosts before round-robin. Without this, a node hitting disk
	// pressure (the trigger for the 2026-05-20 vsock-wedge cascade)
	// keeps receiving new creates until every one of them fails the
	// post-boot health probe — multiplying the visible damage.
	healthCache := StartHealthCache(disc)

	return &FCAgentBackend{
		cfg:         cfg,
		discovery:   disc,
		lb:          NewLoadBalancerWithHealth(disc, healthCache),
		healthCache: healthCache,
		routes:      make(map[string]string),

		portMappings: make(map[string]map[int]*remotePortMapping),
	}, nil
}

// Name returns the backend identifier.
func (b *FCAgentBackend) Name() string { return "firecracker-agent" }

func (b *FCAgentBackend) RunnerCapabilities() sandbox.RunnerCapabilities {
	return sandbox.RunnerCapabilities{
		Target:    b.Name(),
		Placement: sandbox.RunnerPlacementRemoteAgent,
		Health:    sandbox.RunnerHealthRemoteAgent,
		Features: sandbox.RunnerFeatures{
			WorkspaceSnapshot: true,
			PortExposure:      true,
			PersistentShell:   true,
			SSH:               true,
			Volumes:           true,
			ComputerUse:       true,
		},
	}
}

var _ sandbox.PortExposer = (*FCAgentBackend)(nil)
var _ sandbox.BackendTargeter = (*FCAgentBackend)(nil)

// RouteUpdater is the hook this backend uses to persist the
// authoritative `agent_target` for a sandbox whenever the in-memory
// route table converges on a new target via discovery.
//
// Without it, every fcagent pod replacement leaves a stale row in
// sandbox_instances.agent_target — the in-memory `routes` map heals
// itself within seconds (withRoute → discoverRoute → setRoute), but
// the DB row is never rewritten, so:
//   - A gateway pod restart re-seeds the in-memory routes from the
//     stale DB value and re-pins to the dead IP.
//   - Cross-pod gateway HA breaks because peers read the same stale
//     value.
//
// Implementations should treat the call as fire-and-forget (caller
// doesn't block on it). A failed write is logged at the implementor's
// discretion; the in-memory routing keeps the next request working,
// and the next route refresh re-attempts the persist.
type RouteUpdater interface {
	UpdateAgentTarget(ctx context.Context, sandboxID, target string) error
}

// SetRouteUpdater wires the persistence hook. Optional — the backend
// is fully functional without it, just without cross-pod / cross-
// restart agent_target durability. Manager calls this at startup
// after the repo is wired up.
func (b *FCAgentBackend) SetRouteUpdater(u RouteUpdater) {
	b.mu.Lock()
	b.routeUpdater = u
	b.mu.Unlock()
}

// routeFor returns the cached client for a sandbox id. If the local route table
// is empty after a gateway restart, it probes discovered agents directly and
// rebuilds the route when one reports the sandbox.
func (b *FCAgentBackend) routeFor(ctx context.Context, id string) (fcpb.FirecrackerAgentClient, string, error) {
	b.mu.RLock()
	target, ok := b.routes[id]
	b.mu.RUnlock()
	if !ok {
		return b.discoverRoute(ctx, id)
	}
	cli, err := b.discovery.Client(target)
	if err != nil {
		b.clearRoute(id)
		if recoveredCli, recoveredTarget, recoveredErr := b.discoverRoute(ctx, id); recoveredErr == nil {
			return recoveredCli, recoveredTarget, nil
		}
		return nil, target, err
	}
	return cli, target, nil
}

// SeedRoute restores one durable sandbox route from sandbox_instances.agent_target.
func (b *FCAgentBackend) SeedRoute(id, target string) {
	id = strings.TrimSpace(id)
	target = strings.TrimSpace(target)
	if id == "" || target == "" {
		return
	}
	b.setRoute(id, target)
}

// SeedRoutes restores multiple durable sandbox routes.
func (b *FCAgentBackend) SeedRoutes(routes map[string]string) {
	for id, target := range routes {
		b.SeedRoute(id, target)
	}
}

func (b *FCAgentBackend) setRoute(id, target string) {
	b.mu.Lock()
	prev, existed := b.routes[id]
	b.routes[id] = target
	updater := b.routeUpdater
	b.mu.Unlock()

	// Only persist when the value actually changed. Idempotent
	// repeats from re-probes against the same fcagent shouldn't
	// generate DB write traffic. First-time writes (no prior
	// entry) DO trigger the persist — at boot, the in-memory
	// route may be reseeded from a stale DB row, so re-writing
	// the freshly-discovered value is the right move.
	if updater == nil {
		return
	}
	if existed && prev == target {
		return
	}
	// Fire-and-forget so the calling RPC isn't held up by a slow
	// DB write. Use a fresh background context with a tight
	// timeout — if Postgres is hosed, we'd rather drop this
	// write than wedge a sandbox operation. Eventual consistency:
	// the next route refresh will try again.
	go func(sandboxID, newTarget string) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := updater.UpdateAgentTarget(ctx, sandboxID, newTarget); err != nil {
			logger.WithFields(
				"sandbox_id", sandboxID,
				"target", newTarget,
				"error", err.Error(),
			).Warn("fcagent: persist recovered agent_target failed (in-memory route still active)")
		}
	}(id, target)
}

func (b *FCAgentBackend) clearRoute(id string) {
	b.mu.Lock()
	delete(b.routes, id)
	b.mu.Unlock()
}

// discoverRoute probes every discovered agent directly. It does not depend on
// the local route table, so it can recover after gateway restarts when the DB
// row lacks agent_target or a seeded target went stale.
func (b *FCAgentBackend) discoverRoute(ctx context.Context, id string) (fcpb.FirecrackerAgentClient, string, error) {
	targets := b.discovery.Targets()
	if len(targets) == 0 {
		return nil, "", fmt.Errorf("%w: fcagent has no discovered agents for sandbox %q", sandbox.ErrSandboxRouteMissing, id)
	}

	var errs []string
	for _, target := range targets {
		cli, err := b.discovery.Client(target)
		if err != nil {
			errs = append(errs, fmt.Sprintf("dial %s: %v", target, err))
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		resp, err := cli.Status(probeCtx, &fcpb.StatusRequest{Id: id})
		cancel()
		if err == nil && resp != nil {
			b.setRoute(id, target)
			return cli, target, nil
		}
		errs = append(errs, fmt.Sprintf("status %s: %v", target, err))
	}

	return nil, "", fmt.Errorf("%w: fcagent could not recover route for sandbox %q (%s)",
		sandbox.ErrSandboxRouteMissing, id, strings.Join(errs, "; "))
}

func withRoute[T any](
	b *FCAgentBackend,
	ctx context.Context,
	id string,
	fn func(fcpb.FirecrackerAgentClient) (T, error),
) (T, error) {
	var zero T
	cli, _, err := b.routeFor(ctx, id)
	if err != nil {
		return zero, err
	}
	out, err := fn(cli)
	if err == nil {
		return out, nil
	}
	if isVMNotFoundRPCError(err) {
		// The routed agent answered, but it has no VM for this sandbox.
		// Either the route is stale (the VM lives on a different agent)
		// or the VM is genuinely gone (process died, host rebooted). One
		// discovery pass across all agents settles which: if another
		// agent has it, retry there; if nobody has it, return an error
		// wrapped in ErrSandboxRouteMissing that still carries the
		// "VM not found" text, which is exactly the shape
		// isSandboxGoneError in the gateway HTTP layer maps to a
		// terminal 410 instead of a retryable 502. Before this branch,
		// a cached route meant the raw Unknown error escaped unwrapped
		// and the admin UI retried a dead sandbox forever.
		b.clearRoute(id)
		if b.discovery != nil {
			_ = b.discovery.RefreshNow()
		}
		recoveredCli, _, routeErr := b.discoverRoute(ctx, id)
		if routeErr != nil {
			return zero, fmt.Errorf("%v; route_recovery=%w", err, routeErr)
		}
		return fn(recoveredCli)
	}
	if !isRouteRecoverableRPCError(err) {
		return zero, err
	}

	// Stale route: forget it and rediscover. Force a fresh DNS resolve
	// first — the loop's 30s cadence is too coarse to ride out an
	// fcagent rolling restart, where DNS may briefly point only at the
	// dead pod. RefreshNow drops dead targets and picks up the new one
	// without waiting for the next tick.
	b.clearRoute(id)
	if b.discovery != nil {
		_ = b.discovery.RefreshNow()
	}

	// Retry loop covering ~3.5s of route rediscovery. gRPC's client-side
	// retry policy (configured in fcagentServiceConfig) already covers
	// per-RPC UNAVAILABLE with 5 attempts spanning ~3s. This loop's job
	// is the next layer: when the route table itself is stale (e.g. a
	// pod restart shifted the sandbox to a different fcagent target),
	// re-probe agents to rebuild it.
	//
	// Three attempts spaced 500ms, 1s, 2s. Beyond this window the
	// caller's own retry (admin UI poll, client WS reconnect) takes
	// over — pinning a single user request past ~6s combined is
	// counterproductive UX.
	//
	// Each iteration: refresh DNS, redial via discoverRoute, attempt
	// the call. Bail immediately on a non-recoverable error or ctx
	// cancellation.
	backoffs := []time.Duration{
		500 * time.Millisecond,
		1 * time.Second,
		2 * time.Second,
	}
	var lastErr error = err
	for _, wait := range backoffs {
		recoveredCli, _, routeErr := b.discoverRoute(ctx, id)
		if routeErr == nil {
			out, callErr := fn(recoveredCli)
			if callErr == nil {
				return out, nil
			}
			if !isRouteRecoverableRPCError(callErr) {
				return zero, callErr
			}
			lastErr = callErr
		} else {
			lastErr = fmt.Errorf("%v; route_recovery=%w", lastErr, routeErr)
		}
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(wait):
		}
		if b.discovery != nil {
			_ = b.discovery.RefreshNow()
		}
	}
	return zero, lastErr
}

func isRouteRecoverableRPCError(err error) bool {
	code := status.Code(err)
	return code == codes.Unavailable || code == codes.DeadlineExceeded || code == codes.Canceled
}

// isVMNotFoundRPCError reports whether the agent authoritatively said
// it has no VM for this sandbox. Newer agents return a typed
// codes.NotFound; older ones return codes.Unknown with the backend's
// "VM not found for sandbox <id>" text, so match both.
func isVMNotFoundRPCError(err error) bool {
	if err == nil {
		return false
	}
	if status.Code(err) == codes.NotFound {
		return true
	}
	return strings.Contains(err.Error(), "VM not found")
}

// Create provisions a new sandbox on a selected agent. Returns the
// instance with AgentTarget populated to the picked host:port so the
// reconciler can persist it to the sandbox_instances row — that lets
// Exec/Shell/Logs route correctly even after a gateway pod restart
// drops the in-memory route table.
//
// Capacity-aware: if an agent rejects the create with a pool-full
// error (fcagent VM pool exhausted, gRPC ResourceExhausted), the LB
// falls forward to the next agent. Only a final all-targets-failed
// outcome surfaces to the caller. This keeps a single saturated node
// from failing requests when a sibling has free slots.
func (b *FCAgentBackend) Create(ctx context.Context, id string, config sandbox.InstanceConfig) (*sandbox.Instance, error) {
	req := &fcpb.CreateSandboxRequest{
		Id:     id,
		Config: configToProto(config),
	}
	resp, target, err := TryEach(b.lb, ctx, func(ctx context.Context, t string, cli fcpb.FirecrackerAgentClient) (*fcpb.SandboxInstance, bool, error) {
		out, callErr := cli.CreateSandbox(ctx, req)
		if callErr != nil {
			return nil, IsCapacityError(callErr), callErr
		}
		return out, false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("fcagent: create on %s: %w", target, err)
	}
	b.setRoute(id, target)
	inst := instanceFromProto(resp, config)
	if inst != nil {
		inst.AgentTarget = target
	}
	return inst, nil
}

func (b *FCAgentBackend) Exec(ctx context.Context, id string, cmd sandbox.ExecRequest) (*sandbox.ExecResult, error) {
	resp, err := withRoute(b, ctx, id, func(cli fcpb.FirecrackerAgentClient) (*fcpb.ExecResult, error) {
		return cli.Exec(ctx, &fcpb.ExecRequest{
			SandboxId: id,
			Command:   cmd.Command,
			WorkDir:   cmd.WorkDir,
			Env:       cmd.Env,
			TimeoutMs: int32(cmd.Timeout / time.Millisecond),
		})
	})
	if err != nil {
		return nil, err
	}
	return &sandbox.ExecResult{
		ExitCode:   int(resp.ExitCode),
		Stdout:     resp.Stdout,
		Stderr:     resp.Stderr,
		DurationMs: resp.DurationMs,
		TimedOut:   resp.TimedOut,
	}, nil
}

func (b *FCAgentBackend) WriteFile(ctx context.Context, id string, path string, content []byte) error {
	_, err := withRoute(b, ctx, id, func(cli fcpb.FirecrackerAgentClient) (*fcpb.Empty, error) {
		return cli.WriteFile(ctx, &fcpb.WriteFileRequest{SandboxId: id, Path: path, Content: content})
	})
	return err
}

func (b *FCAgentBackend) ReadFile(ctx context.Context, id string, path string) ([]byte, error) {
	resp, err := withRoute(b, ctx, id, func(cli fcpb.FirecrackerAgentClient) (*fcpb.ReadFileResponse, error) {
		return cli.ReadFile(ctx, &fcpb.ReadFileRequest{SandboxId: id, Path: path})
	})
	if err != nil {
		return nil, err
	}
	return resp.Content, nil
}

func (b *FCAgentBackend) ListFiles(ctx context.Context, id string, path string) ([]sandbox.FileInfo, error) {
	resp, err := withRoute(b, ctx, id, func(cli fcpb.FirecrackerAgentClient) (*fcpb.ListFilesResponse, error) {
		return cli.ListFiles(ctx, &fcpb.ListFilesRequest{SandboxId: id, Path: path})
	})
	if err != nil {
		return nil, err
	}
	out := make([]sandbox.FileInfo, 0, len(resp.Files))
	for _, f := range resp.Files {
		out = append(out, sandbox.FileInfo{
			Name:  f.Name,
			Path:  f.Path,
			Size:  f.Size,
			IsDir: f.IsDir,
		})
	}
	return out, nil
}

func (b *FCAgentBackend) Destroy(ctx context.Context, id string) error {
	_, err := withRoute(b, ctx, id, func(cli fcpb.FirecrackerAgentClient) (*fcpb.Empty, error) {
		return cli.DestroySandbox(ctx, &fcpb.DestroySandboxRequest{Id: id})
	})
	if err != nil {
		return err
	}
	b.clearRoute(id)
	b.portMu.Lock()
	delete(b.portMappings, id)
	b.portMu.Unlock()
	return nil
}

func (b *FCAgentBackend) ExposePort(ctx context.Context, id string, port int, protocol string) (int, error) {
	if port <= 0 || port > 65535 {
		return 0, fmt.Errorf("invalid guest port %d", port)
	}
	proto := strings.ToLower(strings.TrimSpace(protocol))
	if proto == "" {
		proto = "tcp"
	}
	if proto != "tcp" {
		return 0, fmt.Errorf("firecracker-agent backend only supports tcp port exposure, got %q", proto)
	}

	b.portMu.Lock()
	if existing, ok := b.portMappings[id][port]; ok {
		hostPort := existing.HostPort
		b.portMu.Unlock()
		return hostPort, nil
	}
	b.portMu.Unlock()

	cli, target, err := b.routeFor(ctx, id)
	if err != nil {
		return 0, err
	}
	resp, err := cli.ExposePort(ctx, &fcpb.ExposePortRequest{
		SandboxId: id,
		Port:      int32(port),
		Protocol:  proto,
	})
	if err != nil {
		return 0, err
	}
	hostPort := int(resp.HostPort)
	if hostPort <= 0 || hostPort > 65535 {
		return 0, fmt.Errorf("firecracker-agent returned invalid host port %d", hostPort)
	}

	b.portMu.Lock()
	if b.portMappings[id] == nil {
		b.portMappings[id] = make(map[int]*remotePortMapping)
	}
	b.portMappings[id][port] = &remotePortMapping{
		Target:   target,
		HostPort: hostPort,
		Protocol: proto,
	}
	b.portMu.Unlock()
	return hostPort, nil
}

func (b *FCAgentBackend) UnexposePort(ctx context.Context, id string, port int) error {
	_, err := withRoute(b, ctx, id, func(cli fcpb.FirecrackerAgentClient) (*fcpb.Empty, error) {
		return cli.UnexposePort(ctx, &fcpb.UnexposePortRequest{
			SandboxId: id,
			Port:      int32(port),
		})
	})
	if err != nil {
		return err
	}
	b.portMu.Lock()
	if mappings := b.portMappings[id]; mappings != nil {
		delete(mappings, port)
		if len(mappings) == 0 {
			delete(b.portMappings, id)
		}
	}
	b.portMu.Unlock()
	return nil
}

func (b *FCAgentBackend) BackendTarget(_ context.Context, id string, port int) (string, error) {
	b.portMu.Lock()
	mapping := b.portMappings[id][port]
	b.portMu.Unlock()
	if mapping == nil {
		return "", fmt.Errorf("no remote port mapping for sandbox %s port %d", id, port)
	}
	host, _, err := net.SplitHostPort(mapping.Target)
	if err != nil {
		return "", fmt.Errorf("invalid firecracker-agent target %q: %w", mapping.Target, err)
	}
	return net.JoinHostPort(host, strconv.Itoa(mapping.HostPort)), nil
}

func (b *FCAgentBackend) Status(ctx context.Context, id string) (*sandbox.Instance, error) {
	resp, err := withRoute(b, ctx, id, func(cli fcpb.FirecrackerAgentClient) (*fcpb.SandboxInstance, error) {
		return cli.Status(ctx, &fcpb.StatusRequest{Id: id})
	})
	if err != nil {
		return nil, err
	}
	inst := instanceFromProto(resp, sandbox.InstanceConfig{})
	b.mu.RLock()
	target := b.routes[id]
	b.mu.RUnlock()
	if inst != nil {
		inst.AgentTarget = target
	}
	return inst, nil
}

// DescribePending returns empty — the remote agent does not expose diagnostics
// through the current proto surface.
func (b *FCAgentBackend) DescribePending(ctx context.Context, id string) string { return "" }

// Healthy verifies at least one agent is reachable. Each per-target probe is
// bounded so a single wedged connection cannot stall callers (e.g. the admin
// overview RPC, which polls every 5s).
//
// On failure the returned error includes the per-target dial / NodeInfo
// errors so logs reveal the underlying cause ("no healthy agents" alone is
// useless — could be DNS, TLS, network policy, the agent itself wedged).
func (b *FCAgentBackend) Healthy(ctx context.Context) error {
	targets := b.discovery.Targets()
	if len(targets) == 0 {
		return errors.New("fcagent: no agents discovered (DNS resolved 0 targets — check headless service endpoints + EVS_SANDBOX_FIRECRACKER_AGENT_SERVICE)")
	}
	var probeErrs []string
	for _, t := range targets {
		cli, err := b.discovery.Client(t)
		if err != nil {
			probeErrs = append(probeErrs, fmt.Sprintf("dial %s: %v", t, err))
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, err = cli.NodeInfo(probeCtx, &fcpb.Empty{})
		cancel()
		if err == nil {
			return nil
		}
		probeErrs = append(probeErrs, fmt.Sprintf("NodeInfo %s: %v", t, err))
	}
	return fmt.Errorf("fcagent: no healthy agents (%d targets probed): %s",
		len(targets), strings.Join(probeErrs, "; "))
}

// Logs streams sandbox logs over gRPC and exposes them as an io.ReadCloser.
func (b *FCAgentBackend) Logs(ctx context.Context, id string, opts sandbox.LogsOptions) (io.ReadCloser, error) {
	req := &fcpb.LogsRequest{
		SandboxId:  id,
		Follow:     opts.Follow,
		Tail:       int32(opts.Tail),
		Timestamps: opts.Timestamps,
	}
	if !opts.Since.IsZero() {
		req.SinceUnix = opts.Since.Unix()
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := withRoute(b, streamCtx, id, func(cli fcpb.FirecrackerAgentClient) (grpc.ServerStreamingClient[fcpb.LogChunk], error) {
		return cli.Logs(streamCtx, req)
	})
	if err != nil {
		cancel()
		return nil, err
	}
	return newLogsReader(stream, cancel), nil
}

func (b *FCAgentBackend) Stats(ctx context.Context, id string) (*sandbox.ContainerStats, error) {
	resp, err := withRoute(b, ctx, id, func(cli fcpb.FirecrackerAgentClient) (*fcpb.SandboxStats, error) {
		return cli.Stats(ctx, &fcpb.StatsRequest{SandboxId: id})
	})
	if err != nil {
		return nil, err
	}
	ts := time.Now()
	if resp.TimestampUnix > 0 {
		ts = time.Unix(resp.TimestampUnix, 0)
	}
	return &sandbox.ContainerStats{
		CPUPercent:     resp.CpuPercent,
		MemoryUsage:    resp.MemoryUsage,
		MemoryLimit:    resp.MemoryLimit,
		MemoryPercent:  resp.MemoryPercent,
		NetworkRxBytes: resp.NetworkRxBytes,
		NetworkTxBytes: resp.NetworkTxBytes,
		BlockRead:      resp.BlockRead,
		BlockWrite:     resp.BlockWrite,
		PIDs:           int(resp.Pids),
		Timestamp:      ts,
	}, nil
}

// Shell opens a bidirectional shell session via the agent. Creates a
// fresh persistent session each time; for reattach, callers should
// use ShellWithSession instead.
func (b *FCAgentBackend) Shell(ctx context.Context, id string, cmd []string) (*sandbox.ShellSession, error) {
	return b.ShellWithSession(ctx, id, "", cmd)
}

// ShellWithSession is the session-aware variant. shellSessionID
// reattaches to a known persistent session; empty creates a new one
// and returns the assigned ID via ShellSession.ShellSessionID.
func (b *FCAgentBackend) ShellWithSession(ctx context.Context, id, shellSessionID string, cmd []string) (*sandbox.ShellSession, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := withRoute(b, streamCtx, id, func(cli fcpb.FirecrackerAgentClient) (grpc.BidiStreamingClient[fcpb.ShellClientMessage, fcpb.ShellServerMessage], error) {
		return cli.Shell(streamCtx)
	})
	if err != nil {
		cancel()
		return nil, err
	}
	if err := stream.Send(&fcpb.ShellClientMessage{
		Msg: &fcpb.ShellClientMessage_Init{Init: &fcpb.ShellInit{
			SandboxId: id,
			Command:   cmd,
			SessionId: shellSessionID,
		}},
	}); err != nil {
		cancel()
		return nil, fmt.Errorf("fcagent: shell init: %w", err)
	}

	// The remote agent sends a ShellSession message as the first
	// server message on a new stream so the client learns the
	// assigned session_id. Read one message up-front to capture it
	// before handing the stream off to shellConn. If the first
	// message is stdout instead (server doesn't support sessions
	// yet, or we're reattaching and the assignment is already
	// known), pass it through.
	first, err := stream.Recv()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("fcagent: shell first recv: %w", err)
	}
	conn := newShellConn(stream, cancel)
	var assigned string
	var reattached bool
	var transport string
	switch m := first.Msg.(type) {
	case *fcpb.ShellServerMessage_Session:
		assigned = m.Session.SessionId
		reattached = m.Session.Reattached
		transport = m.Session.Transport
	default:
		// Buffer the first message back into the conn so the caller
		// sees it. shellConn has an internal queue for this exact
		// case — see queueServerMessage.
		conn.queueServerMessage(first)
	}
	return &sandbox.ShellSession{
		Conn:           conn,
		Resize:         conn.resize,
		ShellSessionID: assigned,
		Reattached:     reattached,
		Transport:      transport,
	}, nil
}

// ListShellSessions queries the remote agent for the persistent
// sessions alive inside a sandbox VM.
func (b *FCAgentBackend) ListShellSessions(ctx context.Context, id string) ([]sandbox.ShellSessionInfo, error) {
	resp, err := withRoute(b, ctx, id, func(cli fcpb.FirecrackerAgentClient) (*fcpb.ListSessionsResponse, error) {
		return cli.ListSessions(ctx, &fcpb.ListSessionsRequest{SandboxId: id})
	})
	if err != nil {
		return nil, err
	}
	out := make([]sandbox.ShellSessionInfo, 0, len(resp.Sessions))
	for _, s := range resp.Sessions {
		idle := int64(-1)
		if resp.NowUnix > 0 && s.LastActivityUnix > 0 {
			idle = resp.NowUnix - s.LastActivityUnix
			if idle < 0 {
				idle = 0
			}
		}
		out = append(out, sandbox.ShellSessionInfo{
			ID:               s.SessionId,
			AttachedClients:  int(s.AttachedClients),
			CreatedUnix:      s.CreatedUnix,
			LastActivityUnix: s.LastActivityUnix,
			IdleSeconds:      idle,
		})
	}
	return out, nil
}

// KillShellSession asks the remote agent to terminate a session.
func (b *FCAgentBackend) KillShellSession(ctx context.Context, id, shellSessionID string) error {
	_, err := withRoute(b, ctx, id, func(cli fcpb.FirecrackerAgentClient) (*fcpb.Empty, error) {
		return cli.KillSession(ctx, &fcpb.KillSessionRequest{
			SandboxId: id,
			SessionId: shellSessionID,
		})
	})
	return err
}

// List aggregates sandboxes across all discovered agents.
func (b *FCAgentBackend) List(ctx context.Context) ([]*sandbox.Instance, error) {
	targets := b.discovery.Targets()
	out := make([]*sandbox.Instance, 0)
	for _, t := range targets {
		cli, err := b.discovery.Client(t)
		if err != nil {
			continue
		}
		resp, err := cli.ListSandboxes(ctx, &fcpb.Empty{})
		if err != nil {
			continue
		}
		for _, i := range resp.Instances {
			inst := instanceFromProto(i, sandbox.InstanceConfig{})
			b.setRoute(inst.ID, t)
			out = append(out, inst)
		}
	}
	return out, nil
}

// --- Converters ---

func configToProto(c sandbox.InstanceConfig) *fcpb.SandboxConfig {
	return &fcpb.SandboxConfig{
		Image:          c.Image,
		CpuLimit:       c.CPULimit,
		MemoryMb:       c.MemoryMB,
		DiskMb:         c.DiskMB,
		TimeoutSeconds: int32(c.TimeoutSeconds),
		NetworkMode:    string(c.NetworkMode),
		AllowedHosts:   c.AllowedHosts,
		EnvVars:        c.EnvVars,
		WorkDir:        c.WorkDir,
		TenantId:       c.TenantID,
		SessionId:      c.SessionID,
		DnsServers:     c.DNSServers,
		Name:           c.Name,
		AgentId:        c.AgentID,
		SshEnabled:     c.SSHEnabled,
	}
}

func instanceFromProto(p *fcpb.SandboxInstance, config sandbox.InstanceConfig) *sandbox.Instance {
	if p == nil {
		return nil
	}
	return &sandbox.Instance{
		ID:           p.Id,
		ContainerID:  p.ContainerId,
		Status:       sandbox.Status(p.Status),
		Config:       config,
		CreatedAt:    time.Unix(p.CreatedAtUnix, 0),
		ExpiresAt:    time.Unix(p.ExpiresAtUnix, 0),
		Backend:      p.Backend,
		Name:         p.Name,
		AgentHealthy: p.AgentHealthy,
	}
}

// buildDialOptions returns the per-dial options including transport
// creds, keepalive params, retry policy, and reconnect backoff. All
// dials to fcagent go through this so the resilience knobs are
// consistent across the discovery pool.
//
// Knobs:
//   - keepalive.ClientParameters{Time:10s, Timeout:3s, PermitWithoutStream:true}
//     The client pings every 10s when idle; a missed PING is declared
//     dead after 3s. Net effect: a dead conn is torn down within ~13s
//     of fcagent disappearing, without waiting for the kernel's
//     much longer TCP keepalive default.
//   - WithDefaultServiceConfig(fcagentServiceConfig): see the const
//     definition above for rationale (auto-retry on UNAVAILABLE for
//     idempotent unary methods + health-check-driven subchannel state).
//   - WithDefaultCallOptions(MaxCallRecv/SendMsgSize): bumped to 64MB
//     so logs/shell-init payloads aren't truncated by the gRPC default.
//   - WithConnectParams: shorter initial backoff (250ms→5s) so a
//     subchannel that goes TRANSIENT_FAILURE doesn't sit dark for the
//     gRPC default 1s→120s window.
func buildDialOptions(tlsCfg TLSConfig) ([]grpc.DialOption, error) {
	common := []grpc.DialOption{
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second,
			Timeout:             3 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.WithDefaultServiceConfig(fcagentServiceConfig),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff: backoff.Config{
				BaseDelay:  250 * time.Millisecond,
				Multiplier: 1.6,
				Jitter:     0.2,
				MaxDelay:   5 * time.Second,
			},
			MinConnectTimeout: 2 * time.Second,
		}),
	}

	if tlsCfg.ClientCert == "" && tlsCfg.ClientKey == "" && tlsCfg.ServerCA == "" {
		return append(common, grpc.WithTransportCredentials(insecure.NewCredentials())), nil
	}
	creds, err := loadClientTLS(tlsCfg)
	if err != nil {
		return nil, err
	}
	return append(common, grpc.WithTransportCredentials(creds)), nil
}
