package v1

import (
	"errors"
	"regexp"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	mcppb "github.com/everstacklabs/everstack/pkg/grpc/everstack/mcp/v1"
)

func expectServerReload(mock sqlmock.Sqlmock, enabled bool) {
	mock.ExpectQuery(regexp.QuoteMeta(`health_last_check, created_at, updated_at`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "name", "url", "transport_type", "auth_type", "auth_config", "tool_filter", "headers", "stdio_config", "enabled", "health_status", "health_last_check", "created_at", "updated_at"}).
			AddRow(int64(7), "tenant-a", "victim", "http://x", "streamable_http", "none", "{}", "{}", "{}", "{}", enabled, "unknown", nil, time.Now(), time.Now()))
}

// Disabling a server takes effect at once: the live entry is gone and the next
// call is refused without reaching the server.
func TestUpdateMcpServer_DisableDropsLiveEntry(t *testing.T) {
	s, srv, mock := victimSetup(t)
	ctx := contextkeys.WithTenantID(t.Context(), "tenant-a")

	mock.ExpectExec(`UPDATE mcp_servers SET`).WillReturnResult(sqlmock.NewResult(0, 1))
	expectServerReload(mock, false)
	disabled := false
	if _, err := s.UpdateMcpServer(ctx, connect.NewRequest(&mcppb.UpdateMcpServerRequest{Id: "7", Enabled: &disabled})); err != nil {
		t.Fatalf("UpdateMcpServer: %v", err)
	}
	if s.registry.OwnedBy("tenant-a", "7") {
		t.Fatal("disabled server is still live")
	}

	mock.ExpectQuery(regexp.QuoteMeta(`FROM mcp_servers WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`)).
		WithArgs(int64(7), "tenant-a").
		WillReturnRows(sqlmock.NewRows([]string{"name", "url", "transport_type", "auth_type", "auth_config", "headers", "stdio_config", "enabled", "tenant_id"}).
			AddRow("victim", srv.URL, "streamable_http", "none", "{}", "{}", "{}", false, "tenant-a"))
	_, err := s.CallTool(ctx, connect.NewRequest(&mcppb.CallToolRequest{ServerId: "7", ToolName: "secret_tool"}))
	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Code() != connect.CodeFailedPrecondition {
		t.Fatalf("CallTool on disabled server = %v, want FailedPrecondition", err)
	}
	if n := srv.toolCalls.Load(); n != 0 {
		t.Fatalf("disabled server received %d tool calls", n)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// A refused update from another tenant leaves the owner's connection alone.
func TestUpdateMcpServer_OtherTenantLeavesLiveEntry(t *testing.T) {
	s, _, mock := victimSetup(t)
	mock.ExpectExec(`UPDATE mcp_servers SET`).WillReturnResult(sqlmock.NewResult(0, 0))

	disabled := false
	_, err := s.UpdateMcpServer(tenantB(), connect.NewRequest(&mcppb.UpdateMcpServerRequest{Id: "7", Enabled: &disabled}))
	wantNotFound(t, err)
	if !s.registry.OwnedBy("tenant-a", "7") {
		t.Fatal("another tenant's update dropped the owner's connection")
	}
}

// "07" parses to the row with id 7, so the delete must close entry "7".
func TestDeleteMcpServer_NonCanonicalIDDropsLiveEntry(t *testing.T) {
	s, _, mock := victimSetup(t)
	ctx := contextkeys.WithTenantID(t.Context(), "tenant-a")
	mock.ExpectExec(`UPDATE mcp_servers SET deleted_at`).
		WithArgs(int64(7), "tenant-a").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if _, err := s.DeleteMcpServer(ctx, connect.NewRequest(&mcppb.DeleteMcpServerRequest{Id: "07"})); err != nil {
		t.Fatalf("DeleteMcpServer: %v", err)
	}
	if s.registry.OwnedBy("tenant-a", "7") {
		t.Fatal("deleted server is still live")
	}
}
