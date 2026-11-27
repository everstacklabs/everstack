package v1

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	sqlmock "github.com/DATA-DOG/go-sqlmock"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/mcp"
	mcppb "github.com/everstacklabs/everstack/pkg/grpc/everstack/mcp/v1"
	"github.com/jmoiron/sqlx"
)

// newSharedModeServer mirrors how startAPI wires a managed gateway: stdio
// is disallowed process-wide and the server context carries the
// SharedGatewayMode marker. db is nil on purpose for the write-path tests:
// if a guard lets a request through, the handler dereferences it and the
// test fails loudly instead of silently passing. The process-wide switch is
// restored when the test ends so later tests see a self-hosted default.
func newSharedModeServer(t *testing.T, db *sqlx.DB) *Server {
	t.Helper()
	t.Cleanup(mcp.SetStdioPolicyForTest(true))
	reg := mcp.NewRegistry(nil)
	reg.DisallowStdio()
	ctx := context.WithValue(context.Background(), contextkeys.SharedGatewayMode, true)
	return &Server{ctx: ctx, db: db, registry: reg}
}

func newSelfHostedServer() *Server {
	return &Server{ctx: context.Background(), registry: mcp.NewRegistry(nil)}
}

func tenantCtx() context.Context {
	return contextkeys.WithTenantID(context.Background(), "tenant-a")
}

func wantCode(t *testing.T, err error, code connect.Code) {
	t.Helper()
	var ce *connect.Error
	if !errors.As(err, &ce) {
		t.Fatalf("expected connect error with code %v, got %v", code, err)
	}
	if ce.Code() != code {
		t.Fatalf("expected code %v, got %v (%v)", code, ce.Code(), err)
	}
}

// wantStdioRefusal pins that every stdio refusal (register, update, lazy
// start) carries the same code and the same customer-facing message.
func wantStdioRefusal(t *testing.T, err error) {
	t.Helper()
	wantCode(t, err, connect.CodeInvalidArgument)
	var ce *connect.Error
	_ = errors.As(err, &ce)
	if ce.Message() != mcp.ErrStdioNotAllowed.Error() {
		t.Fatalf("refusal message = %q, want %q", ce.Message(), mcp.ErrStdioNotAllowed.Error())
	}
}

func stdioCfg() *mcppb.McpStdioConfig {
	return &mcppb.McpStdioConfig{Command: "/bin/sh", Args: []string{"-c", "true"}}
}

func TestRegisterMcpServer_RefusesStdioOnSharedGateway(t *testing.T) {
	s := newSharedModeServer(t, nil)

	// Explicit stdio transport.
	_, err := s.RegisterMcpServer(tenantCtx(), connect.NewRequest(&mcppb.RegisterMcpServerRequest{
		Name:          "evil",
		TransportType: mcppb.McpTransportType_MCP_TRANSPORT_TYPE_STDIO,
		StdioConfig:   stdioCfg(),
		Enabled:       true,
	}))
	wantStdioRefusal(t, err)

	// Stdio config smuggled under a non-stdio transport is refused too: an
	// Update could flip the transport later and the config would run.
	_, err = s.RegisterMcpServer(tenantCtx(), connect.NewRequest(&mcppb.RegisterMcpServerRequest{
		Name:          "sneaky",
		Url:           "http://example.invalid/sse",
		TransportType: mcppb.McpTransportType_MCP_TRANSPORT_TYPE_SSE,
		StdioConfig:   stdioCfg(),
		Enabled:       true,
	}))
	wantStdioRefusal(t, err)
}

func TestUpdateMcpServer_RefusesStdioOnSharedGateway(t *testing.T) {
	s := newSharedModeServer(t, nil)
	stdio := mcppb.McpTransportType_MCP_TRANSPORT_TYPE_STDIO

	_, err := s.UpdateMcpServer(tenantCtx(), connect.NewRequest(&mcppb.UpdateMcpServerRequest{
		Id:            "7",
		TransportType: &stdio,
	}))
	wantStdioRefusal(t, err)

	_, err = s.UpdateMcpServer(tenantCtx(), connect.NewRequest(&mcppb.UpdateMcpServerRequest{
		Id:          "7",
		StdioConfig: stdioCfg(),
	}))
	wantStdioRefusal(t, err)
}

// TestStdioGuard_EitherSignalRefuses pins that the registry switch and the
// SharedGatewayMode context marker are each sufficient on their own, and
// that a self-hosted server (neither set) lets stdio writes through.
func TestStdioGuard_EitherSignalRefuses(t *testing.T) {
	func() {
		defer mcp.SetStdioPolicyForTest(true)()
		registryOnly := &Server{ctx: context.Background(), registry: mcp.NewRegistry(nil)}
		registryOnly.registry.DisallowStdio()
		if registryOnly.stdioAllowed() {
			t.Fatal("registry switch alone should refuse stdio")
		}
		// The registry switch is the process-wide switch: a server with no
		// registry at all is refused too.
		if (&Server{ctx: context.Background()}).stdioAllowed() {
			t.Fatal("process-wide switch should refuse stdio without a registry")
		}
	}()

	ctxOnly := &Server{
		ctx:      context.WithValue(context.Background(), contextkeys.SharedGatewayMode, true),
		registry: mcp.NewRegistry(nil),
	}
	if ctxOnly.stdioAllowed() {
		t.Fatal("SharedGatewayMode context alone should refuse stdio")
	}

	selfHosted := newSelfHostedServer()
	if !selfHosted.stdioAllowed() {
		t.Fatal("self-hosted server should allow stdio")
	}
	if err := selfHosted.checkStdioWrite(true, true); err != nil {
		t.Fatalf("self-hosted stdio write should pass the guard: %v", err)
	}
	// Non-stdio writes never consult the policy.
	if err := newSharedModeServer(t, nil).checkStdioWrite(false, false); err != nil {
		t.Fatalf("non-stdio write should pass the guard on a shared gateway: %v", err)
	}
}

func newMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return sqlx.NewDb(raw, "sqlmock"), mock
}

// stdioRowJSON is a persisted stdio_config whose command leaves a marker
// file if it ever runs.
func stdioRowJSON(marker string) string {
	return `{"command":"/bin/sh","args":["-c","echo ran > ` + marker + `"],"env":{}}`
}

func assertNotExecuted(t *testing.T, marker string) {
	t.Helper()
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("marker %s exists (stat err=%v): the persisted stdio command was executed", marker, err)
	}
}

// TestEnsureServerRegistered_DoesNotStartPreexistingStdioRow covers the
// lazy start paths (DiscoverTools, CallTool, GetMcpServerHealth): a stdio
// row that predates the write guard is loaded from the DB but never
// started on a shared gateway.
func TestEnsureServerRegistered_DoesNotStartPreexistingStdioRow(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	db, mock := newMockDB(t)
	s := newSharedModeServer(t, db)

	mock.ExpectQuery(`SELECT name, url, transport_type, auth_type, auth_config, headers, stdio_config, enabled, tenant_id`).
		WithArgs(int64(42), "tenant-a").
		WillReturnRows(sqlmock.NewRows([]string{"name", "url", "transport_type", "auth_type", "auth_config", "headers", "stdio_config", "enabled", "tenant_id"}).
			AddRow("legacy stdio", "", "stdio", "none", "{}", "{}", stdioRowJSON(marker), true, "tenant-a"))

	err := s.ensureServerRegistered(tenantCtx(), "tenant-a", "42")
	wantStdioRefusal(t, err)
	if _, ok := s.registry.Get("42"); ok {
		t.Fatal("refused stdio server must not be in the live registry")
	}
	assertNotExecuted(t, marker)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
}

// TestHydrateRegistryForTenant_SkipsStdioRowsOnSharedGateway covers the
// bulk start paths (agent session start, deploy invoke, boot-time
// HydrateRegistryAll): stdio rows are skipped without failing the batch.
func TestHydrateRegistryForTenant_SkipsStdioRowsOnSharedGateway(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	db, mock := newMockDB(t)
	s := newSharedModeServer(t, db)

	mock.ExpectQuery(`SELECT id, name, url, transport_type, auth_type, auth_config, headers, stdio_config, tenant_id`).
		WithArgs("tenant-a").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "url", "transport_type", "auth_type", "auth_config", "headers", "stdio_config", "tenant_id"}).
			AddRow(int64(42), "legacy stdio", "", "stdio", "none", "{}", "{}", stdioRowJSON(marker), "tenant-a"))

	if err := s.HydrateRegistryForTenant(tenantCtx(), "tenant-a"); err != nil {
		t.Fatalf("hydration should skip stdio rows, not fail: %v", err)
	}
	if _, ok := s.registry.Get("42"); ok {
		t.Fatal("skipped stdio server must not be in the live registry")
	}
	assertNotExecuted(t, marker)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
}

// TestRegistryLayerRefusesEvenIfHandlerGuardIsBypassed is the defense in
// depth claim: a stdio ServerConfig handed straight to the registry of a
// shared gateway (the path the OAuth re-register helpers take) is refused
// by the registry itself.
func TestRegistryLayerRefusesEvenIfHandlerGuardIsBypassed(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	s := newSharedModeServer(t, nil)
	cfg := buildServerConfig("42", "tenant-a", "legacy stdio", "", "stdio", "none", "{}", "{}", stdioRowJSON(marker))
	_, err := s.registry.Register(context.Background(), cfg)
	if !errors.Is(err, mcp.ErrStdioNotAllowed) {
		t.Fatalf("registry should refuse stdio: %v", err)
	}
	assertNotExecuted(t, marker)
}
