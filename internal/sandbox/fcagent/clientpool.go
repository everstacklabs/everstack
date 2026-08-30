package fcagent

import (
	"fmt"
	"time"
)

// NewClientPool builds a Discovery + LoadBalancer for the Firecracker
// agent fleet from a Config, reusing the same DNS discovery, dial, and
// mTLS setup the sandbox FCAgentBackend uses.
//
// It exists so other subsystems that talk to the SAME agents — notably
// the isolated-functions runtime, which issues InvokeFunction RPCs —
// can reach the fleet without duplicating dial/TLS wiring or standing up
// a second connection pool. The returned Discovery owns background DNS
// refresh and gRPC connections; the caller MUST call Discovery.Stop when
// done.
//
// This deliberately returns only the health-gated round-robin primitives
// (Discovery + LoadBalancer). Sticky per-sandbox routing, agent_target
// persistence, and route recovery live on FCAgentBackend and are
// irrelevant to ephemeral, stateless function dispatch.
func NewClientPool(cfg Config) (*Discovery, *LoadBalancer, error) {
	if cfg.Service == "" {
		return nil, nil, fmt.Errorf("fcagent: Service is required")
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
		return nil, nil, fmt.Errorf("fcagent: tls: %w", err)
	}
	disc, err := NewDiscovery(cfg.Service, cfg.Port, cfg.RefreshInterval, cfg.DialTimeout, dialOpts)
	if err != nil {
		return nil, nil, err
	}
	return disc, NewLoadBalancer(disc), nil
}
