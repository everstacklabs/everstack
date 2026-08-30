package egress

import (
	"context"
	"fmt"
	"sync"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

const (
	// sidecarImage is the Docker image for the egress proxy sidecar.
	// This should be a minimal image with iptables and the egress-proxy binary.
	sidecarImage = "ghcr.io/everstacklabs/egress-proxy:latest"

	// sidecarLabelKey identifies sidecar containers for cleanup.
	sidecarLabelKey = "everstack.egress.sandbox_id"

	// dnsProxyPort is the port the DNS proxy listens on inside the sidecar.
	dnsProxyPort = "15353"
)

// SidecarController manages egress sidecar containers for Docker sandboxes.
// Each sandbox in whitelist mode gets a sidecar container that shares its
// network namespace and intercepts DNS to enforce domain allowlisting.
type SidecarController struct {
	docker   *client.Client
	sidecars sync.Map // sandboxID → sidecar container ID
	audit    AuditSink
}

// NewSidecarController creates a new sidecar egress controller.
func NewSidecarController(dockerClient *client.Client, audit AuditSink) *SidecarController {
	return &SidecarController{
		docker: dockerClient,
		audit:  audit,
	}
}

// Start creates and starts an egress sidecar for the given sandbox.
//
// How it works:
//  1. Sidecar container starts with --network container:{sandboxID} (shared network namespace)
//  2. Sidecar sets iptables rule: OUTPUT -p udp --dport 53 -j REDIRECT --to-ports 15353
//  3. DNS proxy on 127.0.0.1:15353 checks queries against AllowedHosts
//  4. Allowed → resolve normally via upstream; Blocked → NXDOMAIN
func (s *SidecarController) Start(ctx context.Context, sandboxID string, cfg EgressConfig) error {
	if cfg.Mode != EgressWhitelist {
		return nil // only whitelist mode needs a sidecar
	}
	if s == nil || s.docker == nil {
		return fmt.Errorf("egress sidecar docker client not configured")
	}

	// Build allowed hosts as comma-separated env var
	allowedCSV := ""
	for i, h := range cfg.AllowedHosts {
		if i > 0 {
			allowedCSV += ","
		}
		allowedCSV += h
	}

	upstreamCSV := "8.8.8.8:53,1.1.1.1:53"
	if len(cfg.DNSServers) > 0 {
		upstreamCSV = ""
		for i, s := range cfg.DNSServers {
			if i > 0 {
				upstreamCSV += ","
			}
			upstreamCSV += s
		}
	}

	containerCfg := &container.Config{
		Image: sidecarImage,
		Env: []string{
			"EGRESS_ALLOWED_HOSTS=" + allowedCSV,
			"EGRESS_UPSTREAM=" + upstreamCSV,
			"EGRESS_SANDBOX_ID=" + sandboxID,
			"EGRESS_LISTEN_ADDR=127.0.0.1:" + dnsProxyPort,
		},
		Labels: map[string]string{
			sidecarLabelKey: sandboxID,
		},
	}

	hostCfg := &container.HostConfig{
		// Share the sandbox container's network namespace
		NetworkMode: container.NetworkMode("container:" + sandboxID),
		// Need NET_ADMIN for iptables redirect rule
		CapAdd:      []string{"NET_ADMIN"},
		SecurityOpt: []string{"no-new-privileges"},
		AutoRemove:  true,
	}

	sidecarName := "evs-egress-" + sandboxID[:12]

	resp, err := s.docker.ContainerCreate(ctx, containerCfg, hostCfg, nil, nil, sidecarName)
	if err != nil {
		logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
			Warn("egress: failed to create sidecar")
		return fmt.Errorf("create sidecar: %w", err)
	}

	if err := s.docker.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		// Clean up
		_ = s.docker.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
			Warn("egress: failed to start sidecar")
		return fmt.Errorf("start sidecar: %w", err)
	}

	s.sidecars.Store(sandboxID, resp.ID)

	logger.WithFields("sandbox_id", sandboxID, "sidecar_id", resp.ID[:12], "allowed_hosts", len(cfg.AllowedHosts)).
		Info("egress: sidecar started")

	return nil
}

// Stop removes the egress sidecar for the given sandbox.
func (s *SidecarController) Stop(ctx context.Context, sandboxID string) error {
	val, ok := s.sidecars.LoadAndDelete(sandboxID)
	if !ok {
		return nil // no sidecar for this sandbox
	}

	sidecarID := val.(string)
	timeout := 3
	_ = s.docker.ContainerStop(ctx, sidecarID, container.StopOptions{Timeout: &timeout})

	if err := s.docker.ContainerRemove(ctx, sidecarID, container.RemoveOptions{Force: true}); err != nil {
		// AutoRemove should handle this, but log just in case
		logger.WithFields("sandbox_id", sandboxID, "sidecar_id", sidecarID[:12], "error", err.Error()).
			Debug("egress: sidecar cleanup note")
	}

	logger.WithFields("sandbox_id", sandboxID).Debug("egress: sidecar stopped")
	return nil
}

// CleanupOrphans removes any orphaned egress sidecar containers.
// Call on startup to clean up from crashes.
func (s *SidecarController) CleanupOrphans(ctx context.Context) error {
	containers, err := s.docker.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}

	for _, c := range containers {
		if _, ok := c.Labels[sidecarLabelKey]; ok {
			logger.WithFields("container_id", c.ID[:12], "sandbox_id", c.Labels[sidecarLabelKey]).
				Info("egress: cleaning up orphaned sidecar")
			_ = s.docker.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true})
		}
	}
	return nil
}
