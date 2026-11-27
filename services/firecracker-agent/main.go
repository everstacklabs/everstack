// Package main implements the Firecracker Agent — a standalone gRPC service
// that manages Firecracker microVMs on a single host.
//
// This service decouples VM management from the gateway process (Option B),
// allowing the gateway to scale independently from Firecracker capacity.
//
// The agent runs as a DaemonSet on KVM-labeled K8s worker nodes. The gateway
// discovers agent pods via the headless Service and routes sandbox requests
// to the appropriate agent.
//
// Architecture:
//
//	Gateway Pod (any node) → gRPC → Firecracker Agent (KVM node)
//	                                  └── FirecrackerBackend (in-process)
//	                                        └── VM Pool
//	                                              └── MicroVMs
//
// Boot sequence: the gRPC listener opens immediately and a health
// service reports NOT_SERVING. Preflight (kvm + binary + kernel +
// rootfs + writable workdir) runs in a loop; when it goes green the
// backend is initialized, attached to the AgentServer, and health
// flips to SERVING. A re-check loop demotes back to NOT_SERVING on
// regression so the gateway routes elsewhere instead of failing
// creates against a host that lost its rootfs.
//
// Environment variables:
//
//	FC_LISTEN_ADDR       gRPC listen address (default: :9090)
//	FC_METRICS_ADDR      metrics + /logs/ SSE listen address (default: :9091)
//	FC_NODE_IP           node IP to pin wildcard listen addresses to
//	                     (set from the downward API; hostNetwork pods
//	                     otherwise bind every interface on the node)
//	FC_AUTH_TOKEN        shared bearer token callers must present on
//	                     every RPC and on /logs/ (see auth.go)
//	FC_AUTH_ALLOW_UNAUTHENTICATED
//	                     "true" accepts calls without a valid token;
//	                     rollout aid only, default false
//	FC_GUEST_CIDR        /16 the per-VM /30s are carved from
//	                     (default: 10.0.0.0/16; must not overlap the
//	                     host's own networks)
//	FC_BINARY            Firecracker binary path (default: /usr/bin/firecracker)
//	FC_KERNEL            Kernel image path (default: /var/lib/everstack/vmlinux.bin)
//	FC_ROOTFS_DIR        Rootfs directory (default: /var/lib/everstack/rootfs)
//	FC_WORK_DIR          VM work directory (default: /var/lib/everstack/vms)
//	FC_MIN_FREE_DISK_MB  Free space the VM store must retain before a new
//	                     microVM is admitted (default: 2048, 0 disables)
//	FC_POOL_MIN_SIZE     Minimum warm VMs (default: 2)
//	FC_POOL_MAX_SIZE     Maximum warm VMs (default: 10)
//	FC_POOL_MAX_TOTAL    Hard cap on total VMs (default: 50)
//	NODE_NAME            K8s node name (set by downward API)
package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/everstacklabs/everstack/internal/sandbox/firecracker"
	fcpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/firecracker/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

const (
	preflightInitialBackoff = 5 * time.Second
	preflightMaxBackoff     = 30 * time.Second
	preflightRecheckPeriod  = 30 * time.Second

	// readinessName is gated on preflight — readiness probe targets
	// this so the kubelet pulls the pod out of the endpoints list
	// during a rootfs/kernel regression. Demoting it lets the
	// gateway route to a healthy node instead of failing the create.
	readinessName = "firecracker-agent"

	// livenessName is set SERVING the moment the gRPC server starts
	// and never demoted. Liveness probe targets this so a slow
	// preflight (or a regression) does not restart the pod — that
	// would just delete the in-flight retry state and start over.
	livenessName = "firecracker-agent.process"
)

func main() {
	nodeName := envOr("NODE_NAME", "unknown")
	nodeIP := os.Getenv("FC_NODE_IP")

	// Bind narrowly. With hostNetwork a wildcard listen is reachable from
	// every interface on the node, including the per-VM TAP gateways and
	// any overlay. Callers only ever dial the node IP.
	addr, wildcard := resolveListenAddr(envOr("FC_LISTEN_ADDR", ":9090"), nodeIP)
	if wildcard {
		log.Printf("firecracker-agent: WARN gRPC listening on a wildcard address %s (set FC_NODE_IP to pin it to the node IP)", addr)
	}
	metricsAddr, metricsWildcard := resolveListenAddr(envOr("FC_METRICS_ADDR", ":9091"), nodeIP)
	if metricsWildcard {
		log.Printf("firecracker-agent: WARN metrics listening on a wildcard address %s (set FC_NODE_IP to pin it to the node IP)", metricsAddr)
	}

	// Guest address pool. Every per-VM /30, the inter-guest isolation
	// drop and the host port guard derive from this one value, so it
	// must be set before any of them run. Pick a /16 that does not
	// overlap the host's own networks.
	guestCIDR := envOr("FC_GUEST_CIDR", firecracker.DefaultGuestPoolCIDR)
	if err := firecracker.ConfigureGuestPool(guestCIDR); err != nil {
		log.Fatalf("firecracker-agent: FC_GUEST_CIDR: %v", err)
	}
	log.Printf("firecracker-agent: guest pool %s", firecracker.GuestPoolCIDR())

	// Authenticate callers. Fails closed: no token and no explicit opt-in
	// means the agent does not start.
	authCfg, err := loadAuthConfig()
	if err != nil {
		log.Fatalf("%v", err)
	}
	log.Printf("firecracker-agent: auth: %s", authCfg.describe())
	auth := newAuthenticator(authCfg)

	cfg := firecracker.FirecrackerConfig{
		BinaryPath: envOr("FC_BINARY", "/usr/bin/firecracker"),
		KernelPath: envOr("FC_KERNEL", "/var/lib/everstack/vmlinux.bin"),
		RootfsDir:  envOr("FC_ROOTFS_DIR", "/var/lib/everstack/rootfs"),
		WorkDir:    envOr("FC_WORK_DIR", "/var/lib/everstack/vms"),
		Pool: firecracker.VMPoolConfig{
			MinSize:           envInt("FC_POOL_MIN_SIZE", 2),
			MaxSize:           envInt("FC_POOL_MAX_SIZE", 10),
			MaxTotal:          envInt("FC_POOL_MAX_TOTAL", 50),
			IdleTimeout:       5 * time.Minute,
			WarmupOnStart:     envBool("FC_POOL_WARMUP", false),
			ReplenishInterval: 1 * time.Second,
			ReplenishBatch:    3,
		},
		VM: firecracker.VMDefaults{
			MemoryMB:  512,
			VCPUs:     1,
			TimeoutMs: 30000,
		},
		MinFreeDiskMB: int64(envInt("FC_MIN_FREE_DISK_MB", firecracker.DefaultMinFreeDiskMB)),
	}

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", addr, err)
	}

	ready := &readiness{}

	grpcOpts := serverInterceptors(auth, ready)
	if tlsOpt, err := serverCredentials(); err != nil {
		log.Fatalf("tls: %v", err)
	} else if tlsOpt != nil {
		log.Println("firecracker-agent: mTLS enabled")
		grpcOpts = append(grpcOpts, tlsOpt)
	}
	srv := grpc.NewServer(grpcOpts...)

	// Register the agent service with a nil backend; the readiness
	// interceptor blocks every method until SetBackend runs and
	// ready flips true. Reflection + health bypass the gate.
	agentServer := NewAgentServer(nil, nodeName)
	agentServer.SetRootfsReporters(rootfsGCReportersFromEnv())
	fcpb.RegisterFirecrackerAgentServer(srv, agentServer)

	healthSrv := health.NewServer()
	healthpb.RegisterHealthServer(srv, healthSrv)
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	healthSrv.SetServingStatus(readinessName, healthpb.HealthCheckResponse_NOT_SERVING)
	// Liveness reflects "this process is up", independent of preflight.
	healthSrv.SetServingStatus(livenessName, healthpb.HealthCheckResponse_SERVING)

	reflection.Register(srv)

	log.Printf("firecracker-agent: listening on %s (node=%s) — preflight pending", addr, nodeName)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Prometheus metrics + a sidecar HTTP listener for /healthz +
	// /metrics. Separate port from gRPC because Prometheus scrapes
	// HTTP-not-gRPC, and we don't want a misbehaving scraper to
	// affect the gRPC listener's connection budget.
	// VM store headroom next to the create guard's threshold, for the
	// FirecrackerVMStoreHeadroom* alerts. Registered before preflight so
	// the series exist even while the agent is NOT_SERVING.
	if err := firecracker.RegisterVMStoreMetrics(prometheus.DefaultRegisterer, cfg.WorkDir, cfg.MinFreeDiskMB); err != nil {
		log.Printf("firecracker-agent: vmstore metrics not registered: %v", err)
	}
	metricsSrv := startMetricsServer(metricsAddr, agentServer, auth)

	// Host firewall: drop guest-originated traffic to our own ports.
	// Binding to the node IP does not stop a guest from addressing the
	// node IP through its TAP default route, and the per-VM chains only
	// govern FORWARD. Re-asserted periodically because a host firewall
	// reload can flush INPUT. Non-fatal: losing the guard is logged, not
	// an outage.
	go maintainHostPortGuard(ctx, listenPorts(addr, metricsAddr))

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("firecracker-agent: shutting down gracefully...")
		healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
		healthSrv.SetServingStatus(readinessName, healthpb.HealthCheckResponse_NOT_SERVING)
		healthSrv.SetServingStatus(livenessName, healthpb.HealthCheckResponse_NOT_SERVING)
		srv.GracefulStop()
		if metricsSrv != nil {
			shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 3*time.Second)
			_ = metricsSrv.Shutdown(shutdownCtx)
			cancelShutdown()
		}
		cancel()
	}()

	// Readiness is the conjunction of preflight and the guest deny guard;
	// either one failing takes the agent out of rotation.
	gate := newReadyGate(ready, healthSrv)
	go runPreflight(ctx, cfg, agentServer, gate)

	// Guest private-destination deny: the shared iptables chains that keep
	// every guest out of the pod network, the service CIDR, the overlay
	// and the node itself. Installed now and re-asserted periodically;
	// every VM create asserts it again and fails closed without it, and
	// the agent is NOT_SERVING while it is missing.
	go maintainGuestDeny(ctx, gate)

	if err := srv.Serve(lis); err != nil {
		log.Fatalf("gRPC server failed: %v", err)
	}

	log.Println("firecracker-agent: stopped")
}

// runPreflight blocks until preflight succeeds (with capped backoff)
// and then runs a periodic re-check that toggles the health status.
// Backend.New is only called once on first success — re-running it
// would leak goroutines and pool state.
func runPreflight(
	ctx context.Context,
	cfg firecracker.FirecrackerConfig,
	agentServer *AgentServer,
	gate *readyGate,
) {
	backoff := preflightInitialBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		results, err := firecracker.Preflight(cfg)
		if err == nil {
			break
		}
		for _, r := range results {
			if !r.OK {
				log.Printf("firecracker-agent: preflight FAIL %s: %v", r.Name, r.Err)
			}
		}
		log.Printf("firecracker-agent: preflight failed; retrying in %s", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < preflightMaxBackoff {
			backoff *= 2
			if backoff > preflightMaxBackoff {
				backoff = preflightMaxBackoff
			}
		}
	}

	backend, err := firecracker.New(cfg)
	if err != nil {
		// Should be impossible — Preflight already ran the same
		// validators. Still: don't crash the agent. Stay NOT_SERVING
		// and let the re-check loop try again on the next tick.
		log.Printf("firecracker-agent: preflight passed but backend init failed: %v", err)
		return
	}

	// Re-attach to Firecracker processes that remain after an agent restart.
	// With hostPID=true, VMs can outlive the agent process while b.vms remains
	// in memory. Recover reads <workdir>/<id>/state.json and rebuilds b.vms
	// before the gRPC server accepts requests. Recovery is best-effort: failures
	// are logged and the agent continues serving the VMs it can track.
	recoverCtx, recoverCancel := context.WithTimeout(ctx, 30*time.Second)
	if err := backend.Recover(recoverCtx); err != nil {
		log.Printf("firecracker-agent: recovery scan failed: %v", err)
	}
	recoverCancel()

	// Reap function-environment VMs that Recover re-attached to. They are
	// ephemeral and their in-memory registry starts empty on restart, so
	// a survivor would be tracked (reaper-safe) yet unmanaged and leak
	// against the node VM cap. Runs before serving so a fresh invocation
	// cold-starts a clean env.
	sweepCtx, sweepCancel := context.WithTimeout(ctx, 60*time.Second)
	SweepRecoveredFunctionEnvs(sweepCtx, backend)
	sweepCancel()

	agentServer.SetBackend(backend)

	// Isolated-function runtime: persistent execution environments backed
	// by the same tracked-VM path sandboxes use. Reuses the node's
	// Firecracker guest workdir and DNS config; envs idle-reap after the
	// lifetime window. Stopped on ctx.Done alongside the reapers below.
	funcEnvs := NewFunctionEnvManager(backend, defaultGuestWorkDir, nil, 0)
	agentServer.SetFunctionEnvManager(funcEnvs)
	go func() {
		<-ctx.Done()
		funcEnvs.Stop()
	}()

	// The agent-owned orphan reaper removes only Firecracker children of this
	// process that are absent from the backend's tracked set. Ownership-aware
	// filtering preserves tracked long-running VMs.
	reaper := firecracker.StartOrphanReaper(backend, firecracker.DefaultOrphanReaperConfig())
	go func() {
		<-ctx.Done()
		reaper.Stop()
	}()

	// Workdir reconciler periodically sweeps /var/lib/everstack/vms
	// and reaps directories whose VMs are no longer tracked by the
	// backend. Complements OrphanReaper, which handles orphan
	// processes; this handles the corresponding orphan filesystem
	// state (rootfs overlays, leftover vsock sockets, dangling TAP +
	// iptables rules). A partial Create can leave hundreds of MB per
	// orphan and eventually push the host into disk pressure.
	workdirReconciler := firecracker.StartWorkdirReconciler(backend, firecracker.DefaultWorkdirReconcilerConfig())
	go func() {
		<-ctx.Done()
		workdirReconciler.Stop()
	}()

	// Rootfs GC: removes stale images and backups from FC_ROOTFS_DIR,
	// which no other component cleans.
	// Starts after Recover so tracked VMs' images are known, sweeps once
	// now and then hourly. Dry run unless FC_ROOTFS_GC_ENFORCE=true.
	rootfsGC := firecracker.NewRootfsGC(backend, agentServer.RootfsReferences(), rootfsGCConfigFromEnv())
	rootfsGC.Start()
	go func() {
		<-ctx.Done()
		rootfsGC.Stop()
	}()

	log.Println("firecracker-agent: preflight passed")
	gate.setPreflight(true)

	ticker := time.NewTicker(preflightRecheckPeriod)
	defer ticker.Stop()
	preflightOK := true
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			results, err := firecracker.Preflight(cfg)
			if err != nil && preflightOK {
				for _, r := range results {
					if !r.OK {
						log.Printf("firecracker-agent: preflight regressed %s: %v", r.Name, r.Err)
					}
				}
			} else if err == nil && !preflightOK {
				log.Println("firecracker-agent: preflight recovered")
			}
			preflightOK = err == nil
			gate.setPreflight(preflightOK)
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// envDuration parses a Go duration. Unparseable or non-positive values
// fall back, so a typo can never shorten the rootfs GC grace period to 0.
func envDuration(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		log.Printf("firecracker-agent: ignoring %s=%q, using %s", key, v, fallback)
		return fallback
	}
	return d
}

// rootfsGCConfigFromEnv reads the rootfs GC settings:
//
//	FC_ROOTFS_GC_ENFORCE   delete eligible files (default false: dry run, log only)
//	FC_ROOTFS_GC_GRACE     minimum age before a file is eligible (default 24h)
//	FC_ROOTFS_GC_INTERVAL  time between sweeps (default 1h; one sweep runs at start)
//	FC_ROOTFS_GC_KEEP      comma-separated rootfs tags never deleted
//	FC_ROOTFS_GC_DIR       writable mount of FC_ROOTFS_DIR used for deletes
//	                       (FC_ROOTFS_DIR itself is mounted read-only)
func rootfsGCConfigFromEnv() firecracker.RootfsGCConfig {
	cfg := firecracker.DefaultRootfsGCConfig()
	cfg.Dir = strings.TrimSpace(os.Getenv("FC_ROOTFS_GC_DIR"))
	cfg.Enforce = envBool("FC_ROOTFS_GC_ENFORCE", cfg.Enforce)
	cfg.GracePeriod = envDuration("FC_ROOTFS_GC_GRACE", cfg.GracePeriod)
	cfg.Interval = envDuration("FC_ROOTFS_GC_INTERVAL", cfg.Interval)
	for _, tag := range strings.Split(os.Getenv("FC_ROOTFS_GC_KEEP"), ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			cfg.Keep = append(cfg.Keep, tag)
		}
	}
	return cfg
}

// rootfsGCReportersFromEnv reads FC_ROOTFS_GC_REPORTERS, the reporter
// names whose ReportRootfsReferences calls the GC trusts. Empty trusts
// none, so every tag image is kept.
func rootfsGCReportersFromEnv() []string {
	var names []string
	for _, name := range strings.Split(os.Getenv("FC_ROOTFS_GC_REPORTERS"), ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

// startMetricsServer brings up the sidecar HTTP listener that serves
// Prometheus scrapes (/metrics) and a simple operator liveness check
// (/healthz). Returns the *http.Server so main's SIGTERM handler can
// shut it down gracefully.
//
// Failures to bind are logged loudly but do not kill the agent — gRPC
// is the user-visible interface; without metrics we lose observability
// but sandboxes keep working. Better to surface "metrics down" via
// scrape failure alerts than to take the whole pod out of rotation.
func startMetricsServer(addr string, agentServer *AgentServer, auth *authenticator) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	// /logs/{sandboxID} — SSE-streamed sandbox logs. Same content
	// the gRPC Logs RPC serves, exposed over HTTP for browser-
	// direct consumption (EventSource) without a gRPC stack. See
	// logs_sse.go for the wire format + query knobs. Gated by the
	// same bearer token as the gRPC surface: it streams tenant
	// output. /metrics and /healthz stay open for scrapes.
	mux.Handle("/logs/", auth.requireHTTP(http.HandlerFunc(agentServer.handleLogsSSE)))
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second, // big enough for scrape latency under load
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		log.Printf("firecracker-agent: metrics listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("firecracker-agent: metrics listener failed: %v", err)
		}
	}()
	return srv
}
