package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/mcp"
	mcppb "github.com/everstacklabs/everstack/pkg/grpc/everstack/mcp/v1"
)

// fakeMCPServer answers initialize, tools/list and tools/call over
// streamable HTTP and counts the calls that reach it after registration.
type fakeMCPServer struct {
	*httptest.Server
	toolCalls atomic.Int32
	toolLists atomic.Int32
}

func newFakeMCPServer(t *testing.T, tool string) *fakeMCPServer {
	t.Helper()
	f := &fakeMCPServer{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req mcp.JsonRpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		id, _ := json.Marshal(req.ID)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(id) + `,"result":{"protocolVersion":"2025-03-26","capabilities":{},"serverInfo":{"name":"victim","version":"1"}}}`))
		case "tools/list":
			f.toolLists.Add(1)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(id) + `,"result":{"tools":[{"name":"` + tool + `","inputSchema":{"type":"object"}}]}}`))
		case "tools/call":
			f.toolCalls.Add(1)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(id) + `,"result":{"content":[{"type":"text","text":"victim data"}]}}`))
		default:
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(id) + `,"result":{}}`))
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// victimSetup registers server "7", owned by tenant-a, in a registry shared
// with tenant-b, as on a managed gateway where every tenant shares one
// process. The DB holds no row of id 7 for tenant-b.
func victimSetup(t *testing.T) (*Server, *fakeMCPServer, sqlmock.Sqlmock) {
	t.Helper()
	victim := newFakeMCPServer(t, "secret_tool")
	reg := mcp.NewRegistry(nil)
	if _, err := reg.Register(context.Background(), mcp.ServerConfig{
		ID:            "7",
		TenantID:      "tenant-a",
		Name:          "victim",
		URL:           victim.URL,
		TransportType: mcp.TransportStreamableHTTP,
	}); err != nil {
		t.Fatalf("register victim server: %v", err)
	}
	victim.toolLists.Store(0)

	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	s := &Server{db: sqlx.NewDb(rawDB, "postgres"), registry: reg}
	return s, victim, mock
}

func expectOwnedLoadMisses(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(regexp.QuoteMeta(`FROM mcp_servers WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`)).
		WithArgs(int64(7), "tenant-b").
		WillReturnRows(sqlmock.NewRows([]string{"name"}))
}

func wantNotFound(t *testing.T, err error) {
	t.Helper()
	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Code() != connect.CodeNotFound {
		t.Fatalf("err = %v, want NotFound", err)
	}
}

func tenantB() context.Context {
	return contextkeys.WithTenantID(context.Background(), "tenant-b")
}

func TestCallTool_RefusesAnotherTenantsLiveServerByID(t *testing.T) {
	s, victim, mock := victimSetup(t)
	expectOwnedLoadMisses(mock)

	_, err := s.CallTool(tenantB(), connect.NewRequest(&mcppb.CallToolRequest{ServerId: "7", ToolName: "secret_tool"}))
	wantNotFound(t, err)
	if n := victim.toolCalls.Load(); n != 0 {
		t.Fatalf("victim server received %d tool calls from another tenant", n)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCallTool_ToolNameLookupNeverPicksAnotherTenantsServer(t *testing.T) {
	s, victim, mock := victimSetup(t)
	mock.ExpectQuery(regexp.QuoteMeta(`AND t.name = $1 AND s.tenant_id = $2`)).
		WithArgs("secret_tool", "tenant-b").
		WillReturnRows(sqlmock.NewRows([]string{"server_id"}))

	_, err := s.CallTool(tenantB(), connect.NewRequest(&mcppb.CallToolRequest{ToolName: "secret_tool"}))
	wantNotFound(t, err)
	if n := victim.toolCalls.Load(); n != 0 {
		t.Fatalf("victim server received %d tool calls from another tenant", n)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverTools_RefusesAnotherTenantsLiveServer(t *testing.T) {
	s, victim, mock := victimSetup(t)
	expectOwnedLoadMisses(mock)

	_, err := s.DiscoverTools(tenantB(), connect.NewRequest(&mcppb.DiscoverToolsRequest{ServerId: "7"}))
	wantNotFound(t, err)
	if n := victim.toolLists.Load(); n != 0 {
		t.Fatalf("victim server was asked for its tools %d times by another tenant", n)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetMcpServerHealth_RefusesAnotherTenantsLiveServer(t *testing.T) {
	s, _, mock := victimSetup(t)
	expectOwnedLoadMisses(mock)

	_, err := s.GetMcpServerHealth(tenantB(), connect.NewRequest(&mcppb.GetMcpServerHealthRequest{Id: "7"}))
	wantNotFound(t, err)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryTenantScopedLookups(t *testing.T) {
	s, _, _ := victimSetup(t)
	reg := s.registry
	if !reg.OwnedBy("tenant-a", "7") {
		t.Fatal("OwnedBy(owner) = false")
	}
	for _, tenant := range []string{"tenant-b", ""} {
		if reg.OwnedBy(tenant, "7") {
			t.Fatalf("OwnedBy(%q) = true for tenant-a's server", tenant)
		}
		if id, ok := reg.FindServerForToolForTenant(tenant, "secret_tool"); ok {
			t.Fatalf("FindServerForToolForTenant(%q) = %q, want no match", tenant, id)
		}
	}
	if id, ok := reg.FindServerForToolForTenant("tenant-a", "secret_tool"); !ok || id != "7" {
		t.Fatalf("FindServerForToolForTenant(owner) = (%q, %v), want (7, true)", id, ok)
	}
}
