package v1

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/mcp"
)

// ensureServerRegistered lazily restores an MCP server from DB into the
// in-memory registry. This keeps persisted servers usable across process
// restarts.
func (s *Server) ensureServerRegistered(ctx context.Context, tenantID, serverID string) error {
	if serverID == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("server_id is required"))
	}

	if _, ok := s.registry.Get(serverID); ok {
		return nil
	}

	serverIDInt := mustParseInt64(serverID)
	if serverIDInt == 0 {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid server_id: %q", serverID))
	}

	var (
		name, serverURL, transportType, authType, authConfigJSON, headersJSON, stdioConfigJSON string
		ownerTenantID                                                                          string
		enabled                                                                                bool
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT name, url, transport_type, auth_type, auth_config, headers, stdio_config, enabled, tenant_id
		 FROM mcp_servers WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		serverIDInt, tenantID,
	).Scan(&name, &serverURL, &transportType, &authType, &authConfigJSON, &headersJSON, &stdioConfigJSON, &enabled, &ownerTenantID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return connect.NewError(connect.CodeNotFound, errors.New("mcp server not found"))
		}
		return connect.NewError(connect.CodeInternal, fmt.Errorf("load mcp server for registry: %w", err))
	}

	if !enabled {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("mcp server %q is disabled", serverID))
	}

	// Another request may have restored the server while we were loading from DB.
	if _, ok := s.registry.Get(serverID); ok {
		return nil
	}

	cfg := buildServerConfig(serverID, ownerTenantID, name, serverURL, transportType, authType, authConfigJSON, headersJSON, stdioConfigJSON)
	if cfg.AuthConfig != nil && cfg.AuthConfig.Type == mcp.AuthTypeOAuth2 {
		if cfg.AuthConfig.AccessToken == "" {
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("mcp server %q requires OAuth authorization before use", serverID))
		}
		cfg.OnTokenUpdate = s.makeTokenUpdateCallback(serverIDInt, tenantID)
	}

	if _, err := s.registry.Register(ctx, cfg); err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("register mcp server in live registry: %w", err))
	}

	logger.Info("mcp server restored to live registry",
		"server_id", serverID,
		"tenant_id", tenantID,
		"name", name,
	)
	return nil
}

// HydrateRegistryForTenant restores every enabled MCP server owned by the
// tenant into the in-memory registry. Idempotent: servers already present
// are skipped, so callers can invoke this on every agent-session start
// without re-handshaking healthy connections.
//
// Why this exists: ensureServerRegistered hydrates one server at a time on
// explicit RPCs (DiscoverTools, CallTool from the MCP page). The agent
// runtime calls registry.FederatedToolsForTenant directly with no
// hydration, so after a gateway restart the in-memory map is empty and
// every agent session sees zero MCP tools — the agent then truthfully
// reports "I don't have access to MCP servers" even though the user has
// configured plenty.
//
// Errors are reported per-server and aggregated; one bad server doesn't
// block the others.
func (s *Server) HydrateRegistryForTenant(ctx context.Context, tenantID string) error {
	if tenantID == "" {
		return errors.New("tenant id is required")
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, url, transport_type, auth_type, auth_config, headers, stdio_config, tenant_id
		 FROM mcp_servers
		 WHERE tenant_id = $1 AND deleted_at IS NULL AND enabled = true`,
		tenantID,
	)
	if err != nil {
		return fmt.Errorf("load enabled mcp servers for tenant %q: %w", tenantID, err)
	}
	defer rows.Close()

	type serverRow struct {
		id, name, url, transport, authType, authJSON, headersJSON, stdioJSON, ownerTenantID string
	}
	var pending []serverRow
	for rows.Next() {
		var (
			id                                                                                int64
			name, url, transport, authType, authJSON, headersJSON, stdioJSON, ownerTenantID string
		)
		if err := rows.Scan(&id, &name, &url, &transport, &authType, &authJSON, &headersJSON, &stdioJSON, &ownerTenantID); err != nil {
			return fmt.Errorf("scan mcp_servers row: %w", err)
		}
		idStr := strconv.FormatInt(id, 10)
		// Skip servers already in the live registry.
		if _, ok := s.registry.Get(idStr); ok {
			continue
		}
		pending = append(pending, serverRow{
			id: idStr, name: name, url: url, transport: transport, authType: authType,
			authJSON: authJSON, headersJSON: headersJSON, stdioJSON: stdioJSON,
			ownerTenantID: ownerTenantID,
		})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate mcp_servers rows: %w", err)
	}

	var firstErr error
	registered := 0
	for _, sr := range pending {
		cfg := buildServerConfig(sr.id, sr.ownerTenantID, sr.name, sr.url, sr.transport, sr.authType, sr.authJSON, sr.headersJSON, sr.stdioJSON)
		if cfg.AuthConfig != nil && cfg.AuthConfig.Type == mcp.AuthTypeOAuth2 {
			if cfg.AuthConfig.AccessToken == "" {
				// OAuth server that hasn't completed authorization yet —
				// silently skip; user will see it in the MCP page as
				// "needs auth", and the existing ensureServerRegistered
				// path will register it on the next OAuth-completion
				// callback.
				continue
			}
			id64, _ := strconv.ParseInt(sr.id, 10, 64)
			cfg.OnTokenUpdate = s.makeTokenUpdateCallback(id64, tenantID)
		}
		if _, err := s.registry.Register(ctx, cfg); err != nil {
			logger.WithFields(
				"server_id", sr.id,
				"tenant_id", tenantID,
				"name", sr.name,
				"error", err.Error(),
			).Warn("mcp: failed to hydrate server into live registry")
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		registered++
	}
	if registered > 0 {
		logger.WithFields(
			"tenant_id", tenantID,
			"hydrated", registered,
		).Info("mcp: hydrated tenant servers into live registry")
	}
	return firstErr
}

// HydrateRegistryAll restores every enabled MCP server across every tenant
// into the in-memory registry. Intended for boot-time use so persisted
// servers are usable as soon as the gateway is up, without waiting for
// each tenant's first session to trigger lazy hydration.
func (s *Server) HydrateRegistryAll(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT tenant_id FROM mcp_servers
		 WHERE deleted_at IS NULL AND enabled = true AND tenant_id IS NOT NULL AND tenant_id <> ''`,
	)
	if err != nil {
		return fmt.Errorf("list tenants with mcp servers: %w", err)
	}
	defer rows.Close()

	var tenantIDs []string
	for rows.Next() {
		var tid string
		if err := rows.Scan(&tid); err != nil {
			return fmt.Errorf("scan tenant_id: %w", err)
		}
		if tid != "" {
			tenantIDs = append(tenantIDs, tid)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	var firstErr error
	for _, tid := range tenantIDs {
		if err := s.HydrateRegistryForTenant(ctx, tid); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// resolveServerIDForToolFromDB resolves the owning server from persisted tool
// records when the live registry is empty.
func (s *Server) resolveServerIDForToolFromDB(ctx context.Context, tenantID, toolName string) (string, error) {
	var serverID int64
	err := s.db.QueryRowContext(ctx,
		`SELECT t.server_id
		 FROM mcp_server_tools t
		 JOIN mcp_servers s ON s.id = t.server_id
		 WHERE s.deleted_at IS NULL AND s.enabled = true AND t.name = $1
		 ORDER BY t.last_seen_at DESC
		 LIMIT 1`,
		toolName,
	).Scan(&serverID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", connect.NewError(connect.CodeNotFound, fmt.Errorf("no server found for tool %q", toolName))
		}
		return "", connect.NewError(connect.CodeInternal, fmt.Errorf("resolve server for tool %q: %w", toolName, err))
	}
	return strconv.FormatInt(serverID, 10), nil
}
