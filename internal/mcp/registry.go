package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ServerEntry holds the live state for a registered MCP server.
type ServerEntry struct {
	Config       ServerConfig
	Client       *Client
	Tools        []ToolInfo
	HealthStatus HealthStatus
	LastHealthAt time.Time
	Enabled      bool
}

// HealthStatus mirrors the proto enum for in-memory use.
type HealthStatus string

const (
	HealthStatusHealthy   HealthStatus = "healthy"
	HealthStatusUnhealthy HealthStatus = "unhealthy"
	HealthStatusUnknown   HealthStatus = "unknown"
)

// Registry manages live MCP server connections, tool caching, and health
// state. Modeled on the provider Registry pattern from gateway/provider.go.
type Registry struct {
	mu      sync.RWMutex
	servers map[string]*ServerEntry // keyed by server ID
	logger  *slog.Logger
}

// NewRegistry creates an empty MCP server registry.
func NewRegistry(logger *slog.Logger) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	return &Registry{
		servers: make(map[string]*ServerEntry),
		logger:  logger.With("component", "mcp_registry"),
	}
}

// DisallowStdio makes Register refuse any server whose transport is stdio,
// regardless of where its config came from. Intended for gateways that
// serve more than one tenant, where a tenant-supplied command would run
// inside the shared gateway process. It flips the process-wide switch (see
// the package-level DisallowStdio), so the transport constructors below
// this registry refuse too; the switch is one-way for the life of the
// process.
func (r *Registry) DisallowStdio() {
	DisallowStdio()
	r.logger.Info("mcp registry: stdio transport disabled for this process")
}

// StdioAllowed reports whether stdio-transport servers may be registered.
func (r *Registry) StdioAllowed() bool {
	return StdioAllowed()
}

// Register adds a server to the registry, establishes a connection,
// initializes the MCP protocol, and discovers tools.
func (r *Registry) Register(ctx context.Context, cfg ServerConfig) (*ServerEntry, error) {
	// Policy gate first: NewClient would otherwise build a stdio transport
	// and Initialize would exec the configured command.
	if cfg.TransportType == TransportStdio && !r.StdioAllowed() {
		r.logger.Warn("mcp registry: refusing stdio server",
			"server_id", cfg.ID,
			"tenant_id", cfg.TenantID,
			"name", cfg.Name,
		)
		return nil, fmt.Errorf("mcp registry: register %q: %w", cfg.Name, ErrStdioNotAllowed)
	}

	client, err := NewClient(cfg, r.logger)
	if err != nil {
		return nil, fmt.Errorf("mcp registry: create client for %q: %w", cfg.Name, err)
	}

	if _, err := client.Initialize(ctx); err != nil {
		client.Close()
		return nil, fmt.Errorf("mcp registry: initialize %q: %w", cfg.Name, err)
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("mcp registry: discover tools for %q: %w", cfg.Name, err)
	}

	entry := &ServerEntry{
		Config:       cfg,
		Client:       client,
		Tools:        tools,
		HealthStatus: HealthStatusHealthy,
		LastHealthAt: time.Now(),
		Enabled:      true,
	}

	r.mu.Lock()
	r.servers[cfg.ID] = entry
	r.mu.Unlock()

	r.logger.Info("mcp server registered",
		"server_id", cfg.ID,
		"name", cfg.Name,
		"tool_count", len(tools),
	)
	return entry, nil
}

// Unregister removes a server and closes its connection.
func (r *Registry) Unregister(id string) {
	r.mu.Lock()
	entry, ok := r.servers[id]
	delete(r.servers, id)
	r.mu.Unlock()
	if ok && entry.Client != nil {
		entry.Client.Close()
	}
}

// Get returns a server entry by ID.
func (r *Registry) Get(id string) (*ServerEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.servers[id]
	return e, ok
}

// All returns all registered server entries.
func (r *Registry) All() []*ServerEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entries := make([]*ServerEntry, 0, len(r.servers))
	for _, e := range r.servers {
		entries = append(entries, e)
	}
	return entries
}

// FederatedTools returns all tools across all enabled servers.
//
// DEPRECATED for multi-tenant callers: this method does NOT filter by
// tenant. Use FederatedToolsForTenant in any code path that runs on
// behalf of a specific tenant (e.g. agent runtime). Kept for admin /
// diagnostic surfaces.
func (r *Registry) FederatedTools() []FederatedTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []FederatedTool
	for _, entry := range r.servers {
		if !entry.Enabled {
			continue
		}
		for _, tool := range entry.Tools {
			result = append(result, FederatedTool{
				ServerID:   entry.Config.ID,
				ServerName: entry.Config.Name,
				Tool:       tool,
			})
		}
	}
	return result
}

// FederatedToolsForTenant returns tools only from servers owned by the given
// tenant. Empty tenantID returns no results — fail-closed to prevent leaks
// from callers that forgot to scope.
func (r *Registry) FederatedToolsForTenant(tenantID string) []FederatedTool {
	if tenantID == "" {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []FederatedTool
	for _, entry := range r.servers {
		if !entry.Enabled {
			continue
		}
		if entry.Config.TenantID != tenantID {
			continue
		}
		for _, tool := range entry.Tools {
			result = append(result, FederatedTool{
				ServerID:   entry.Config.ID,
				ServerName: entry.Config.Name,
				Tool:       tool,
			})
		}
	}
	return result
}

// CallTool routes a tool call to the appropriate server.
//
// DEPRECATED for multi-tenant callers: does not verify ownership. Use
// CallToolForTenant in any code path acting on behalf of a tenant.
func (r *Registry) CallTool(ctx context.Context, serverID, toolName string, args map[string]interface{}) (*ToolCallResult, error) {
	entry, ok := r.Get(serverID)
	if !ok {
		return nil, fmt.Errorf("mcp registry: server %q not found", serverID)
	}
	if !entry.Enabled {
		return nil, fmt.Errorf("mcp registry: server %q is disabled", serverID)
	}
	return entry.Client.CallTool(ctx, toolName, args)
}

// CallToolForTenant routes a tool call only when the server is owned by the
// given tenant. Fails closed on missing tenantID, missing server, disabled
// server, or tenant mismatch.
func (r *Registry) CallToolForTenant(ctx context.Context, tenantID, serverID, toolName string, args map[string]interface{}) (*ToolCallResult, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("mcp registry: tenant id required")
	}
	entry, ok := r.Get(serverID)
	if !ok {
		return nil, fmt.Errorf("mcp registry: server %q not found", serverID)
	}
	if entry.Config.TenantID != tenantID {
		return nil, fmt.Errorf("mcp registry: server %q not found", serverID)
	}
	if !entry.Enabled {
		return nil, fmt.Errorf("mcp registry: server %q is disabled", serverID)
	}
	return entry.Client.CallTool(ctx, toolName, args)
}

// FindServerForToolForTenant looks up which of the tenant's servers owns a
// tool by name. The registry holds every tenant's servers on a shared
// gateway, so an unscoped lookup would route one tenant's call to another
// tenant's server. Empty tenantID finds nothing (fail closed).
func (r *Registry) FindServerForToolForTenant(tenantID, toolName string) (string, bool) {
	if tenantID == "" {
		return "", false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, entry := range r.servers {
		if !entry.Enabled || entry.Config.TenantID != tenantID {
			continue
		}
		for _, t := range entry.Tools {
			if t.Name == toolName {
				return entry.Config.ID, true
			}
		}
	}
	return "", false
}

// OwnedBy reports whether serverID is registered and owned by tenantID.
// Empty tenantID owns nothing (fail closed).
func (r *Registry) OwnedBy(tenantID, serverID string) bool {
	if tenantID == "" {
		return false
	}
	entry, ok := r.Get(serverID)
	return ok && entry.Config.TenantID == tenantID
}

// RefreshTools re-discovers tools for a given server.
func (r *Registry) RefreshTools(ctx context.Context, serverID string) ([]ToolInfo, error) {
	entry, ok := r.Get(serverID)
	if !ok {
		return nil, fmt.Errorf("mcp registry: server %q not found", serverID)
	}

	tools, err := entry.Client.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("mcp registry: refresh tools for %q: %w", serverID, err)
	}

	r.mu.Lock()
	entry.Tools = tools
	r.mu.Unlock()

	return tools, nil
}

// UpdateHealth updates the health status for a server.
func (r *Registry) UpdateHealth(serverID string, status HealthStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry, ok := r.servers[serverID]; ok {
		entry.HealthStatus = status
		entry.LastHealthAt = time.Now()
	}
}

// FederatedTool pairs a tool with its owning server metadata.
type FederatedTool struct {
	ServerID   string
	ServerName string
	Tool       ToolInfo
}
