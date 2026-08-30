package fcagent

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	fcpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/firecracker/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
)

// Discovery resolves agent pods via DNS (K8s headless service) and maintains
// a pool of gRPC client connections.
type Discovery struct {
	service     string
	port        int
	refresh     time.Duration
	dialTimeout time.Duration
	dialOpts    []grpc.DialOption

	// If Service contains a comma (e.g. "10.0.0.1:9090,10.0.0.2:9090"),
	// it is parsed as a static list rather than resolved via DNS.
	static []string

	mu      sync.RWMutex
	targets []string
	conns   map[string]*grpc.ClientConn
	stopCh  chan struct{}
}

// NewDiscovery creates a discovery instance and performs an initial resolve.
func NewDiscovery(service string, port int, refresh, dialTimeout time.Duration, dialOpts []grpc.DialOption) (*Discovery, error) {
	d := &Discovery{
		service:     service,
		port:        port,
		refresh:     refresh,
		dialTimeout: dialTimeout,
		dialOpts:    dialOpts,
		conns:       make(map[string]*grpc.ClientConn),
		stopCh:      make(chan struct{}),
	}

	if strings.Contains(service, ",") {
		parts := strings.Split(service, ",")
		for i, p := range parts {
			parts[i] = strings.TrimSpace(p)
		}
		d.static = parts
	}

	// A transient DNS miss at boot (e.g. the headless Service has no endpoints
	// yet because the DaemonSet is still rolling out) must not permanently
	// disable the sandbox feature. Log and let the refresh loop recover.
	if err := d.resolve(); err != nil {
		logger.WithFields("service", service, "err", err.Error()).Warn(
			"fcagent: initial resolve failed; will retry in background",
		)
	}
	go d.loop()
	return d, nil
}

func (d *Discovery) loop() {
	t := time.NewTicker(d.refresh)
	defer t.Stop()
	for {
		select {
		case <-d.stopCh:
			return
		case <-t.C:
			_ = d.resolve()
		}
	}
}

// Stop halts the discovery refresh loop and closes all connections.
func (d *Discovery) Stop() {
	close(d.stopCh)
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.conns {
		_ = c.Close()
	}
	d.conns = make(map[string]*grpc.ClientConn)
}

// resolve queries DNS (or uses the static list) and updates the target set.
func (d *Discovery) resolve() error {
	var targets []string
	if len(d.static) > 0 {
		targets = append(targets, d.static...)
	} else {
		ips, err := net.LookupHost(d.service)
		if err != nil {
			return fmt.Errorf("fcagent discovery: lookup %s: %w", d.service, err)
		}
		for _, ip := range ips {
			targets = append(targets, net.JoinHostPort(ip, strconv.Itoa(d.port)))
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// Close connections to targets that no longer exist.
	seen := make(map[string]struct{}, len(targets))
	for _, t := range targets {
		seen[t] = struct{}{}
	}
	for t, c := range d.conns {
		if _, ok := seen[t]; !ok {
			_ = c.Close()
			delete(d.conns, t)
		}
	}
	d.targets = targets
	return nil
}

// RefreshNow forces an immediate DNS resolve, bypassing the loop's
// 30-second cadence. Used by recovery paths (e.g. withRoute on a
// stale route) so a freshly-rolled fcagent pod gets picked up
// without waiting up to a full refresh interval. Safe to call from
// any goroutine; serialized by d.mu inside resolve().
func (d *Discovery) RefreshNow() error {
	return d.resolve()
}

// Targets returns a snapshot of the current agent target addresses.
func (d *Discovery) Targets() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]string, len(d.targets))
	copy(out, d.targets)
	return out
}

// Client returns a lazily-dialed gRPC client for the given target.
// Reuses a cached *grpc.ClientConn when its state is healthy
// (Idle/Connecting/Ready). When the cached conn has fallen to
// TransientFailure or Shutdown — typical after the pod behind a
// stable IP restarts faster than DNS rotation can be observed — the
// conn is closed and a new one dialed.
//
// Without this check, a same-IP pod-restart leaves the gateway
// dispatching every RPC onto a dead subchannel. gRPC's own retry
// policy partially papers over this (the retry attempts request a
// reconnect), but a fresh dial is materially faster and avoids the
// "first request after restart always 502s" tail.
func (d *Discovery) Client(target string) (fcpb.FirecrackerAgentClient, error) {
	d.mu.RLock()
	conn, ok := d.conns[target]
	d.mu.RUnlock()
	if ok && isUsableConn(conn) {
		return fcpb.NewFirecrackerAgentClient(conn), nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if existing, ok := d.conns[target]; ok {
		if isUsableConn(existing) {
			return fcpb.NewFirecrackerAgentClient(existing), nil
		}
		// Cached but dead — close and fall through to redial. Close
		// is best-effort; even if it errors we want a fresh conn.
		_ = existing.Close()
		delete(d.conns, target)
		logger.WithFields("target", target).Debug(
			"fcagent discovery: closed broken conn, redialing",
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), d.dialTimeout)
	defer cancel()
	conn, err := grpc.DialContext(ctx, target, d.dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("fcagent discovery: dial %s: %w", target, err)
	}
	d.conns[target] = conn
	return fcpb.NewFirecrackerAgentClient(conn), nil
}

// isUsableConn returns true when a cached gRPC client conn is still in
// a state that can carry RPCs. Idle/Connecting/Ready are all fine —
// gRPC will queue the request and resolve transparently. Only
// TransientFailure and Shutdown indicate the conn won't recover on its
// own in a useful window, and should be torn down.
//
// Note: Idle is treated as usable because gRPC promotes Idle→Ready on
// first use. Closing an Idle conn just because no RPC has touched it
// in a while would be counterproductive.
func isUsableConn(conn *grpc.ClientConn) bool {
	if conn == nil {
		return false
	}
	switch conn.GetState() {
	case connectivity.TransientFailure, connectivity.Shutdown:
		return false
	default:
		return true
	}
}
