package v1

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/mcp"
	mcppb "github.com/everstacklabs/everstack/pkg/grpc/everstack/mcp/v1"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ─── RegisterMcpServer ──────────────────────────────────────────────

func (s *Server) RegisterMcpServer(ctx context.Context, req *connect.Request[mcppb.RegisterMcpServerRequest]) (*connect.Response[mcppb.RegisterMcpServerResponse], error) {
	tenantID, err := s.resolveTenantID(ctx, req.Msg.TenantId)
	if err != nil {
		return nil, err
	}

	if req.Msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}

	transportType := protoTransportToString(req.Msg.TransportType)
	authType := protoAuthTypeToString(req.Msg.AuthType)
	authConfig := structToJSON(req.Msg.AuthConfig)
	toolFilter := toolFilterToJSON(req.Msg.ToolFilter)
	headers := mapToJSON(req.Msg.Headers)
	stdioConfig := stdioConfigToJSON(req.Msg.StdioConfig)

	// Insert into database
	var serverID int64
	err = s.db.QueryRowContext(ctx,
		`INSERT INTO mcp_servers (tenant_id, name, url, transport_type, auth_type, auth_config, tool_filter, headers, stdio_config, enabled)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 RETURNING id`,
		tenantID, req.Msg.Name, req.Msg.Url, transportType, authType, authConfig, toolFilter, headers, stdioConfig, req.Msg.Enabled,
	).Scan(&serverID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("insert mcp server: %w", err))
	}

	// Register in the live registry and discover tools
	cfg := mcp.ServerConfig{
		ID:            strconv.FormatInt(serverID, 10),
		TenantID:      tenantID,
		Name:          req.Msg.Name,
		URL:           req.Msg.Url,
		TransportType: mcp.TransportType(transportType),
		Headers:       req.Msg.Headers,
		AuthConfig:    buildAuthConfig(authType, req.Msg.AuthConfig),
	}
	if req.Msg.StdioConfig != nil {
		cfg.StdioConfig = &mcp.StdioConfig{
			Command:    req.Msg.StdioConfig.Command,
			Args:       req.Msg.StdioConfig.Args,
			Env:        req.Msg.StdioConfig.Env,
			WorkingDir: req.Msg.StdioConfig.WorkingDir,
		}
	}

	// For OAuth2 servers, skip the initial connection — tokens aren't available
	// until the user completes the browser OAuth flow. The server will be
	// re-registered in the live registry after the OAuth callback.
	var protoTools []*mcppb.McpTool
	if authType == "oauth2" && (cfg.AuthConfig == nil || cfg.AuthConfig.AccessToken == "") {
		logger.Info("mcp server registered; awaiting OAuth authorization before connecting", "server_id", serverID)
	} else {
		entry, regErr := s.registry.Register(ctx, cfg)
		if regErr == nil && entry != nil {
			for _, t := range entry.Tools {
				if err := s.persistTool(ctx, serverID, t); err != nil {
					logger.Error("failed to persist tool", "error", err, "tool", t.Name)
				}
				protoTools = append(protoTools, toolInfoToProto(strconv.FormatInt(serverID, 10), t))
			}
		} else if regErr != nil {
			logger.Warn("mcp server registered but connection failed; will retry via health check", "error", regErr)
		}
	}

	// Emit CQRS event
	s.publishEvent(ctx, "mcp.server.registered", map[string]interface{}{
		"server_id": serverID,
		"tenant_id": tenantID,
		"name":      req.Msg.Name,
	})

	server, err := s.loadServerFromDB(ctx, serverID, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("load registered server: %w", err))
	}

	return connect.NewResponse(&mcppb.RegisterMcpServerResponse{
		Server: server,
		Tools:  protoTools,
	}), nil
}

// ─── GetMcpServer ───────────────────────────────────────────────────

func (s *Server) GetMcpServer(ctx context.Context, req *connect.Request[mcppb.GetMcpServerRequest]) (*connect.Response[mcppb.GetMcpServerResponse], error) {
	tenantID, err := s.resolveTenantID(ctx, req.Msg.TenantId)
	if err != nil {
		return nil, err
	}

	server, err := s.loadServerFromDB(ctx, mustParseInt64(req.Msg.Id), tenantID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&mcppb.GetMcpServerResponse{Server: server}), nil
}

// ─── ListMcpServers ─────────────────────────────────────────────────

func (s *Server) ListMcpServers(ctx context.Context, req *connect.Request[mcppb.ListMcpServersRequest]) (*connect.Response[mcppb.ListMcpServersResponse], error) {
	tenantID, err := s.resolveTenantID(ctx, req.Msg.TenantId)
	if err != nil {
		return nil, err
	}

	// `s.tenant_id = $1` is the post-2026-05-06 P0 fix: previously this
	// query returned every MCP server in the database regardless of
	// caller. The schema's `idx_mcp_servers_tenant_enabled` covers the
	// (tenant_id, enabled) WHERE deleted_at IS NULL predicate.
	query := `SELECT s.id, s.tenant_id, s.name, s.url, s.transport_type, s.auth_type, s.auth_config, s.tool_filter, s.headers, s.stdio_config, s.enabled, s.health_status, s.health_last_check, s.created_at, s.updated_at,
	                 COALESCE(t.tool_count, 0) AS tool_count
	          FROM mcp_servers s
	          LEFT JOIN (SELECT server_id, COUNT(*)::int AS tool_count FROM mcp_server_tools GROUP BY server_id) t ON t.server_id = s.id
	          WHERE s.tenant_id = $1 AND s.deleted_at IS NULL`
	args := []interface{}{tenantID}
	if req.Msg.EnabledOnly {
		query += ` AND s.enabled = true`
	}
	query += ` ORDER BY s.created_at DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list mcp servers: %w", err))
	}
	defer rows.Close()

	var servers []*mcppb.McpServer
	for rows.Next() {
		srv, err := scanServerRowWithToolCount(rows)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scan server row: %w", err))
		}
		servers = append(servers, srv)
	}

	return connect.NewResponse(&mcppb.ListMcpServersResponse{Servers: servers}), nil
}

// ─── UpdateMcpServer ────────────────────────────────────────────────

func (s *Server) UpdateMcpServer(ctx context.Context, req *connect.Request[mcppb.UpdateMcpServerRequest]) (*connect.Response[mcppb.UpdateMcpServerResponse], error) {
	tenantID, err := s.resolveTenantID(ctx, req.Msg.TenantId)
	if err != nil {
		return nil, err
	}

	serverID := mustParseInt64(req.Msg.Id)

	// Build dynamic UPDATE
	sets := []string{}
	args := []interface{}{}
	argIdx := 1

	addSet := func(col string, val interface{}) {
		sets = append(sets, fmt.Sprintf("%s = $%d", col, argIdx))
		args = append(args, val)
		argIdx++
	}

	if req.Msg.Name != nil {
		addSet("name", *req.Msg.Name)
	}
	if req.Msg.Url != nil {
		addSet("url", *req.Msg.Url)
	}
	if req.Msg.TransportType != nil {
		addSet("transport_type", protoTransportToString(*req.Msg.TransportType))
	}
	if req.Msg.AuthType != nil {
		addSet("auth_type", protoAuthTypeToString(*req.Msg.AuthType))
	}
	if req.Msg.AuthConfig != nil {
		addSet("auth_config", structToJSON(req.Msg.AuthConfig))
	}
	if req.Msg.ToolFilter != nil {
		addSet("tool_filter", toolFilterToJSON(req.Msg.ToolFilter))
	}
	if len(req.Msg.Headers) > 0 {
		addSet("headers", mapToJSON(req.Msg.Headers))
	}
	if req.Msg.StdioConfig != nil {
		addSet("stdio_config", stdioConfigToJSON(req.Msg.StdioConfig))
	}
	if req.Msg.Enabled != nil {
		addSet("enabled", *req.Msg.Enabled)
	}
	addSet("updated_at", time.Now())

	if len(sets) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("no fields to update"))
	}

	query := "UPDATE mcp_servers SET "
	for i, s := range sets {
		if i > 0 {
			query += ", "
		}
		query += s
	}
	// Tenant predicate on the WHERE makes it impossible for tenant A
	// to mutate tenant B's row by guessing its id; pre-fix the SQL
	// only constrained on `id = $N` and trusted the auth check, which
	// is the same shape of leak the 2026-05-06 P0 closed elsewhere.
	query += fmt.Sprintf(" WHERE id = $%d AND tenant_id = $%d AND deleted_at IS NULL", argIdx, argIdx+1)
	args = append(args, serverID, tenantID)

	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("update mcp server: %w", err))
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("mcp server not found"))
	}

	// Emit event
	s.publishEvent(ctx, "mcp.server.updated", map[string]interface{}{
		"server_id": serverID,
		"tenant_id": tenantID,
	})

	server, err := s.loadServerFromDB(ctx, serverID, tenantID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&mcppb.UpdateMcpServerResponse{Server: server}), nil
}

// ─── DeleteMcpServer ────────────────────────────────────────────────

func (s *Server) DeleteMcpServer(ctx context.Context, req *connect.Request[mcppb.DeleteMcpServerRequest]) (*connect.Response[mcppb.DeleteMcpServerResponse], error) {
	tenantID, err := s.resolveTenantID(ctx, req.Msg.TenantId)
	if err != nil {
		return nil, err
	}

	serverID := mustParseInt64(req.Msg.Id)

	// Soft delete, scoped to the caller's tenant. Pre-fix this query
	// only constrained on id, so any caller could delete any tenant's
	// MCP server by guessing the id.
	result, err := s.db.ExecContext(ctx,
		`UPDATE mcp_servers SET deleted_at = NOW() WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
		serverID, tenantID,
	)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("delete mcp server: %w", err))
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("mcp server not found"))
	}

	// Remove from live registry
	s.registry.Unregister(req.Msg.Id)

	// Emit event
	s.publishEvent(ctx, "mcp.server.deleted", map[string]interface{}{
		"server_id": serverID,
		"tenant_id": tenantID,
	})

	return connect.NewResponse(&mcppb.DeleteMcpServerResponse{}), nil
}

// ─── Helpers ────────────────────────────────────────────────────────

func (s *Server) persistTool(ctx context.Context, serverID int64, t mcp.ToolInfo) error {
	inputSchema, _ := json.Marshal(t.InputSchema)
	annotations, _ := json.Marshal(t.Annotations)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO mcp_server_tools (server_id, name, description, input_schema, annotations)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (server_id, name) DO UPDATE SET
		   description = EXCLUDED.description,
		   input_schema = EXCLUDED.input_schema,
		   annotations = EXCLUDED.annotations,
		   last_seen_at = NOW()`,
		serverID, t.Name, t.Description, string(inputSchema), string(annotations),
	)
	return err
}

// loadServerFromDB fetches a server row by id, scoped to the caller's
// tenant. Empty tenantID is rejected as not-found rather than running
// an unscoped query — the previous version trusted the handler's auth
// check and returned (and let callers display) any tenant's server
// row by id.
func (s *Server) loadServerFromDB(ctx context.Context, id int64, tenantID string) (*mcppb.McpServer, error) {
	if tenantID == "" {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("mcp server not found"))
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, name, url, transport_type, auth_type, auth_config, tool_filter, headers, stdio_config, enabled, health_status, health_last_check, created_at, updated_at
		 FROM mcp_servers WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, id, tenantID)

	srv, err := scanSingleRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("mcp server not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("load server: %w", err))
	}

	// Tool count — server_id alone is sufficient because we've already
	// proven (above) that this server belongs to the caller's tenant.
	var toolCount int32
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM mcp_server_tools WHERE server_id = $1`, id).Scan(&toolCount)
	srv.ToolCount = toolCount

	return srv, nil
}

// scannable is an interface for sql.Row and sql.Rows.
type scannable interface {
	Scan(dest ...interface{}) error
}

func scanServerRow(rows *sql.Rows) (*mcppb.McpServer, error) {
	return scanServer(rows, false)
}

func scanServerRowWithToolCount(rows *sql.Rows) (*mcppb.McpServer, error) {
	return scanServer(rows, true)
}

func scanSingleRow(row *sql.Row) (*mcppb.McpServer, error) {
	return scanServer(row, false)
}

func scanServer(s scannable, withToolCount bool) (*mcppb.McpServer, error) {
	var (
		srv             mcppb.McpServer
		authConfigJSON  string
		toolFilterJSON  string
		headersJSON     string
		stdioConfigJSON string
		healthLastCheck sql.NullTime
		createdAt       time.Time
		updatedAt       time.Time
		transportType   string
		authType        string
		healthStatus    string
		id              int64
	)

	dest := []interface{}{&id, &srv.TenantId, &srv.Name, &srv.Url, &transportType, &authType, &authConfigJSON, &toolFilterJSON, &headersJSON, &stdioConfigJSON, &srv.Enabled, &healthStatus, &healthLastCheck, &createdAt, &updatedAt}
	if withToolCount {
		dest = append(dest, &srv.ToolCount)
	}

	if err := s.Scan(dest...); err != nil {
		return nil, err
	}

	srv.Id = strconv.FormatInt(id, 10)
	srv.TransportType = stringToProtoTransport(transportType)
	srv.AuthType = stringToProtoAuthType(authType)
	srv.HealthStatus = stringToProtoHealthStatus(healthStatus)
	srv.CreatedAt = timestamppb.New(createdAt)
	srv.UpdatedAt = timestamppb.New(updatedAt)
	if healthLastCheck.Valid {
		srv.HealthLastCheck = timestamppb.New(healthLastCheck.Time)
	}

	if authConfigJSON != "" && authConfigJSON != "{}" {
		srv.AuthConfig = jsonToStruct(authConfigJSON)
	}
	if toolFilterJSON != "" && toolFilterJSON != "{}" {
		srv.ToolFilter = jsonToToolFilter(toolFilterJSON)
	}
	if headersJSON != "" && headersJSON != "{}" {
		srv.Headers = jsonToMap(headersJSON)
	}
	if stdioConfigJSON != "" && stdioConfigJSON != "{}" {
		srv.StdioConfig = jsonToStdioConfig(stdioConfigJSON)
	}

	return &srv, nil
}

// ─── Proto <-> DB Converters ────────────────────────────────────────

func protoTransportToString(t mcppb.McpTransportType) string {
	switch t {
	case mcppb.McpTransportType_MCP_TRANSPORT_TYPE_SSE:
		return "sse"
	case mcppb.McpTransportType_MCP_TRANSPORT_TYPE_STREAMABLE_HTTP:
		return "streamable_http"
	case mcppb.McpTransportType_MCP_TRANSPORT_TYPE_STDIO:
		return "stdio"
	default:
		return "sse"
	}
}

func stringToProtoTransport(s string) mcppb.McpTransportType {
	switch s {
	case "sse":
		return mcppb.McpTransportType_MCP_TRANSPORT_TYPE_SSE
	case "streamable_http":
		return mcppb.McpTransportType_MCP_TRANSPORT_TYPE_STREAMABLE_HTTP
	case "stdio":
		return mcppb.McpTransportType_MCP_TRANSPORT_TYPE_STDIO
	default:
		return mcppb.McpTransportType_MCP_TRANSPORT_TYPE_UNSPECIFIED
	}
}

func protoAuthTypeToString(t mcppb.McpAuthType) string {
	switch t {
	case mcppb.McpAuthType_MCP_AUTH_TYPE_NONE:
		return "none"
	case mcppb.McpAuthType_MCP_AUTH_TYPE_API_KEY:
		return "api_key"
	case mcppb.McpAuthType_MCP_AUTH_TYPE_BEARER:
		return "bearer"
	case mcppb.McpAuthType_MCP_AUTH_TYPE_OAUTH2:
		return "oauth2"
	default:
		return "none"
	}
}

func stringToProtoAuthType(s string) mcppb.McpAuthType {
	switch s {
	case "none":
		return mcppb.McpAuthType_MCP_AUTH_TYPE_NONE
	case "api_key":
		return mcppb.McpAuthType_MCP_AUTH_TYPE_API_KEY
	case "bearer":
		return mcppb.McpAuthType_MCP_AUTH_TYPE_BEARER
	case "oauth2":
		return mcppb.McpAuthType_MCP_AUTH_TYPE_OAUTH2
	default:
		return mcppb.McpAuthType_MCP_AUTH_TYPE_UNSPECIFIED
	}
}

func stringToProtoHealthStatus(s string) mcppb.McpServerHealthStatus {
	switch s {
	case "healthy":
		return mcppb.McpServerHealthStatus_MCP_SERVER_HEALTH_STATUS_HEALTHY
	case "unhealthy":
		return mcppb.McpServerHealthStatus_MCP_SERVER_HEALTH_STATUS_UNHEALTHY
	default:
		return mcppb.McpServerHealthStatus_MCP_SERVER_HEALTH_STATUS_UNKNOWN
	}
}

func structToJSON(s *structpb.Struct) string {
	if s == nil {
		return "{}"
	}
	b, _ := json.Marshal(s.AsMap())
	return string(b)
}

func jsonToStruct(s string) *structpb.Struct {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	st, _ := structpb.NewStruct(m)
	return st
}

func toolFilterToJSON(tf *mcppb.McpToolFilter) string {
	if tf == nil {
		return "{}"
	}
	b, _ := json.Marshal(map[string]interface{}{"type": tf.Type, "tools": tf.Tools})
	return string(b)
}

func jsonToToolFilter(s string) *mcppb.McpToolFilter {
	var m struct {
		Type  string   `json:"type"`
		Tools []string `json:"tools"`
	}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	return &mcppb.McpToolFilter{Type: m.Type, Tools: m.Tools}
}

func mapToJSON(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func jsonToMap(s string) map[string]string {
	var m map[string]string
	_ = json.Unmarshal([]byte(s), &m)
	return m
}

func stdioConfigToJSON(cfg *mcppb.McpStdioConfig) string {
	if cfg == nil {
		return "{}"
	}
	b, _ := json.Marshal(map[string]interface{}{
		"command":     cfg.Command,
		"args":        cfg.Args,
		"env":         cfg.Env,
		"working_dir": cfg.WorkingDir,
	})
	return string(b)
}

func jsonToStdioConfig(s string) *mcppb.McpStdioConfig {
	var m struct {
		Command    string            `json:"command"`
		Args       []string          `json:"args"`
		Env        map[string]string `json:"env"`
		WorkingDir string            `json:"working_dir"`
	}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	return &mcppb.McpStdioConfig{
		Command:    m.Command,
		Args:       m.Args,
		Env:        m.Env,
		WorkingDir: m.WorkingDir,
	}
}

func toolInfoToProto(serverID string, t mcp.ToolInfo) *mcppb.McpTool {
	tool := &mcppb.McpTool{
		ServerId:    serverID,
		Name:        t.Name,
		Description: t.Description,
	}
	if len(t.InputSchema) > 0 {
		tool.InputSchema, _ = structpb.NewStruct(t.InputSchema)
	}
	if len(t.Annotations) > 0 {
		tool.Annotations, _ = structpb.NewStruct(t.Annotations)
	}
	return tool
}

func mustParseInt64(s string) int64 {
	id, _ := strconv.ParseInt(s, 10, 64)
	return id
}

// buildAuthConfig converts proto auth fields into the transport-layer AuthConfig.
func buildAuthConfig(authType string, authConfigStruct *structpb.Struct) *mcp.AuthConfig {
	at := mcp.AuthType(authType)
	if at == mcp.AuthTypeNone || at == "" {
		return nil
	}

	cfg := &mcp.AuthConfig{Type: at}

	if authConfigStruct == nil {
		return cfg
	}
	m := authConfigStruct.AsMap()

	switch at {
	case mcp.AuthTypeBearer:
		if tok, ok := m["token"].(string); ok {
			cfg.Token = tok
		}

	case mcp.AuthTypeAPIKey:
		if tok, ok := m["token"].(string); ok {
			cfg.Token = tok
		}
		if hdr, ok := m["api_key_header"].(string); ok {
			cfg.APIKeyHeader = hdr
		}

	case mcp.AuthTypeOAuth2:
		if tok, ok := m["access_token"].(string); ok {
			cfg.AccessToken = tok
		}
		if tok, ok := m["refresh_token"].(string); ok {
			cfg.RefreshToken = tok
		}
		if url, ok := m["token_url"].(string); ok {
			cfg.TokenURL = url
		}
		if cid, ok := m["client_id"].(string); ok {
			cfg.ClientID = cid
		}
		if secret, ok := m["client_secret"].(string); ok {
			cfg.ClientSecret = secret
		}
	}

	return cfg
}

// buildServerConfig constructs a ServerConfig from DB-loaded fields. Used when
// restoring servers from the database on startup.
func buildServerConfig(id, tenantID string, name, url, transportType, authType, authConfigJSON, headersJSON, stdioConfigJSON string) mcp.ServerConfig {
	cfg := mcp.ServerConfig{
		ID:            id,
		TenantID:      tenantID,
		Name:          name,
		URL:           url,
		TransportType: mcp.TransportType(transportType),
	}

	// Parse headers
	if headersJSON != "" && headersJSON != "{}" {
		var headers map[string]string
		_ = json.Unmarshal([]byte(headersJSON), &headers)
		cfg.Headers = headers
	}

	// Parse stdio config
	if stdioConfigJSON != "" && stdioConfigJSON != "{}" {
		var stdio mcp.StdioConfig
		_ = json.Unmarshal([]byte(stdioConfigJSON), &stdio)
		cfg.StdioConfig = &stdio
	}

	// Parse auth config
	if authType != "none" && authType != "" {
		var authMap map[string]interface{}
		if authConfigJSON != "" && authConfigJSON != "{}" {
			_ = json.Unmarshal([]byte(authConfigJSON), &authMap)
		}
		var authStruct *structpb.Struct
		if len(authMap) > 0 {
			authStruct, _ = structpb.NewStruct(authMap)
		}
		cfg.AuthConfig = buildAuthConfig(authType, authStruct)
	}

	return cfg
}
