// Package egress provides per-sandbox egress controls.
// It implements DNS-proxy-based domain allowlisting for Docker sandboxes
// using a sidecar container pattern.
package egress

import "context"

// EgressMode controls outbound network behavior.
type EgressMode string

const (
	EgressDeny      EgressMode = "deny"      // No outbound network
	EgressAllow     EgressMode = "allow"     // Unrestricted outbound
	EgressWhitelist EgressMode = "whitelist" // Only allowed domains
)

// EgressConfig configures egress control for a sandbox.
type EgressConfig struct {
	Mode         EgressMode `json:"mode"`
	AllowedHosts []string   `json:"allowed_hosts,omitempty"`
	DNSServers   []string   `json:"dns_servers,omitempty"`
}

// Controller manages egress enforcement for a sandbox.
type Controller interface {
	// Start enables egress controls for the given sandbox container.
	Start(ctx context.Context, sandboxID string, cfg EgressConfig) error

	// Stop removes egress controls and cleans up resources.
	Stop(ctx context.Context, sandboxID string) error
}

// AuditEvent represents an egress decision for audit logging.
type AuditEvent struct {
	SandboxID string `json:"sandbox_id"`
	Domain    string `json:"domain"`
	Action    string `json:"action"` // "allowed" or "blocked"
	QueryType string `json:"query_type"`
}

// AuditSink receives egress audit events.
type AuditSink interface {
	// Emit records an egress audit event.
	Emit(event AuditEvent)
}
